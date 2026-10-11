package main

// smartfactory instance init: one customer's own server, split off a server that runs several.
//
//	smartfactory instance init -from /opt/smartfactory -company huaduan -dir /opt/smartfactory/instances/huaduan \
//	    -http 127.0.0.1:44731 -mqtt :41884
//
// It writes the instance's config (that company only, its equipment, its history database, the
// same administrators' tokens, MQTT users with new passwords) and copies that company's data and
// nothing else: the simulation state, its run-time config, its assistants, documents, notes,
// tickets and conversations, its knowledge base, and its part of the audit log. The databases
// are copied with VACUUM INTO, so the server they come from can keep running.

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	_ "modernc.org/sqlite"

	"smartfactory/internal/config"
)

func runInstance(args []string) error {
	if len(args) == 0 || args[0] != "init" {
		return errors.New("usage: smartfactory instance init -from DIR -company ID -dir DIR -http ADDR -mqtt ADDR")
	}
	fs := flag.NewFlagSet("instance init", flag.ExitOnError)
	from := fs.String("from", "/opt/smartfactory", "directory of the server the company runs on now (its smartfactory.json and data)")
	company := fs.String("company", "", "the company the instance runs")
	dir := fs.String("dir", "", "the instance's directory")
	httpAddr := fs.String("http", "", "where the instance listens, e.g. 127.0.0.1:44731")
	mqtt := fs.String("mqtt", "", "its MQTT broker, e.g. :41884 (empty: none)")
	fs.Parse(args[1:])
	if *company == "" || *dir == "" || *httpAddr == "" {
		return errors.New("-company, -dir and -http are required")
	}
	if _, err := os.Stat(filepath.Join(*dir, "smartfactory.json")); err == nil {
		return fmt.Errorf("%s already has an instance", *dir)
	}
	src, err := config.Load(filepath.Join(*from, "smartfactory.json"))
	if err != nil {
		return err
	}
	srcData := src.Data
	if srcData == "" {
		srcData = "data"
	}
	if !filepath.IsAbs(srcData) {
		srcData = filepath.Join(*from, srcData)
	}
	raw, _ := os.ReadFile(filepath.Join(*from, "smartfactory.json"))
	var orig config.Config
	json.Unmarshal(raw, &orig) // as written: ${VAR} and SF_TOKEN not expanded into the new file
	c := *company

	out := config.Config{HTTP: *httpAddr, Data: "data", Companies: []string{c}, Token: orig.Token}
	for _, k := range orig.Tokens {
		if len(k.Companies) == 0 || slices.Contains(k.Companies, "*") || slices.Contains(k.Companies, c) {
			out.Tokens = append(out.Tokens, k)
		}
	}
	if *mqtt != "" {
		out.MQTT = config.Broker{Listen: *mqtt, Prefix: orig.MQTT.Prefix, Public: orig.MQTT.Public, Users: map[string]string{}}
		// the same users, new passwords: the equipment of this company is moved to this broker
		for u := range orig.MQTT.Users {
			b := make([]byte, 16)
			rand.Read(b)
			out.MQTT.Users[u] = hex.EncodeToString(b)
		}
	}
	for _, s := range orig.Sources {
		if s.Company != "" {
			if s.Company == c {
				out.Sources = append(out.Sources, s)
			}
			continue
		}
		// a source mapped point by point: the points of this company
		s.Points = slices.DeleteFunc(slices.Clone(s.Points), func(p config.Point) bool { return p.Company != c })
		s.Commands = slices.DeleteFunc(slices.Clone(s.Commands), func(b config.CmdBinding) bool { return b.Company != c })
		if len(s.Points) > 0 || len(s.Commands) > 0 {
			out.Sources = append(out.Sources, s)
		}
	}
	for _, h := range orig.History {
		if h.Company == c {
			out.History = append(out.History, h)
		}
	}

	data := filepath.Join(*dir, "data")
	for _, d := range []string{data, filepath.Join(data, "configs", "companies"), filepath.Join(data, "configs", "industries"), filepath.Join(data, "agents"), filepath.Join(data, "knowledge")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	// the simulation's state and run-time configs
	copied := []string{}
	cp := func(rel string) error {
		if err := copyFile(filepath.Join(srcData, rel), filepath.Join(data, rel)); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		copied = append(copied, rel)
		return nil
	}
	for _, rel := range []string{c + ".json", filepath.Join("configs", "companies", c+".json"), filepath.Join("knowledge", c+".process.json")} {
		if err := cp(rel); err != nil {
			return err
		}
	}
	inds, _ := filepath.Glob(filepath.Join(srcData, "configs", "industries", "*.json"))
	for _, f := range inds {
		if err := cp(filepath.Join("configs", "industries", filepath.Base(f))); err != nil {
			return err
		}
	}
	// the databases: a consistent copy, then everything not this company's goes
	type db struct {
		rel   string
		prune []string
	}
	dbs := []db{
		{"agents/agents.db", []string{
			`DELETE FROM agents WHERE company != ?1`, `DELETE FROM docs WHERE company != ?1`, `DELETE FROM notes WHERE company != ?1`,
			`DELETE FROM tickets WHERE company != ?1`, `DELETE FROM conversations WHERE company != ?1`, `DELETE FROM seeded WHERE company != ?1`,
		}},
		{"audit.db", []string{`DELETE FROM audit WHERE company != ?1`}},
		// tokens of every company stay (the platform's administrators); those of other companies go
		{"accounts.db", []string{`DELETE FROM tokens WHERE companies != '' AND ',' || companies || ',' NOT LIKE '%,' || ?1 || ',%'`}},
		{"knowledge/" + c + ".db", nil},
	}
	for _, d := range dbs {
		srcDB := filepath.Join(srcData, d.rel)
		if _, err := os.Stat(srcDB); err != nil {
			continue
		}
		dst := filepath.Join(data, d.rel)
		if err := snapshot(srcDB, dst, c, d.prune); err != nil {
			return fmt.Errorf("%s: %w", d.rel, err)
		}
		copied = append(copied, d.rel)
	}

	b, _ := json.MarshalIndent(out, "", "  ")
	if err := os.WriteFile(filepath.Join(*dir, "smartfactory.json"), b, 0o600); err != nil {
		return err
	}
	fmt.Printf("instance of %s in %s: http %s, mqtt %s, %d sources, %d history, copied %s\n",
		c, *dir, *httpAddr, cmpOr(*mqtt, "none"), len(out.Sources), len(out.History), strings.Join(copied, ", "))
	return nil
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// snapshot copies a SQLite database consistently while it is in use, then runs prune on the copy
// (with the company as ?1) and compacts it, so nothing deleted can be read back from free pages.
func snapshot(from, to, company string, prune []string) error {
	s, err := sql.Open("sqlite", from+"?_pragma=busy_timeout(10000)")
	if err != nil {
		return err
	}
	defer s.Close()
	if _, err := s.Exec(`VACUUM INTO ?`, to); err != nil {
		return err
	}
	d, err := sql.Open("sqlite", to+"?_pragma=foreign_keys(1)")
	if err != nil {
		return err
	}
	defer d.Close()
	for _, q := range prune {
		if _, err := d.Exec(q, company); err != nil {
			return fmt.Errorf("%s: %w", q, err)
		}
	}
	if len(prune) > 0 {
		_, err = d.Exec(`VACUUM`)
	}
	return err
}
