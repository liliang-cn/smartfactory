// Command tablereplay plays a camera box on a restaurant's floor: it makes up a day of table
// states (lunch and dinner rushes, a late supper) and reports them to a SmartFactory server, the
// day so far at once and then each one at its time.
//
//	SF_TOKEN=<reporter token> tablereplay -url http://127.0.0.1:4719 -company qianyu -plant chunxi
//
// The token is read from SF_TOKEN (or -token-file) and only ever sent in the Authorization header.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"slices"
	"strings"
	"time"

	"smartfactory/internal/tables"
	"smartfactory/internal/tables/replay"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "tablereplay:", err)
		os.Exit(1)
	}
}

func run() error {
	base := flag.String("url", "http://127.0.0.1:4719", "the server")
	company := flag.String("company", "", "company id")
	plant := flag.String("plant", "", "only this plant's tables (default all)")
	only := flag.String("tables", "", "only these tables, comma-separated (default all)")
	day := flag.String("day", "", "the day to play, YYYY-MM-DD (default today)")
	source := flag.String("source", "cam-replay", "what the reports say saw them")
	follow := flag.Bool("follow", true, "after the day so far, report each state at its time until the day ends")
	tokenFile := flag.String("token-file", "", "read the token from this file instead of SF_TOKEN")
	seed := flag.Uint64("seed", 1, "the made-up day's seed")
	flag.Parse()
	if *company == "" {
		return errors.New("-company is required")
	}
	token := os.Getenv("SF_TOKEN")
	if *tokenFile != "" {
		b, err := os.ReadFile(*tokenFile)
		if err != nil {
			return err
		}
		token = strings.TrimSpace(string(b))
	}
	if token == "" {
		return errors.New("no token: set SF_TOKEN or -token-file (a reporter token of the company)")
	}
	when := time.Now()
	if *day != "" {
		d, err := time.ParseInLocation("2006-01-02", *day, time.Local)
		if err != nil {
			return errors.New("-day is YYYY-MM-DD")
		}
		when = d.Add(12 * time.Hour)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	c := &replay.Client{Base: strings.TrimRight(*base, "/"), Company: *company, Token: token}
	all, err := c.Tables(ctx)
	if err != nil {
		return fmt.Errorf("reading the tables: %w", err)
	}
	ids := strings.Split(*only, ",")
	var list []replay.Table
	for _, t := range all {
		if (*plant == "" || t.Plant == *plant) && (*only == "" || slices.Contains(ids, t.ID)) {
			list = append(list, t)
		}
	}
	if len(list) == 0 {
		return errors.New("no tables to play: check -company, -plant and -tables")
	}
	reports := replay.Day(list, when, *source, *seed)
	now := time.Now()
	sofar := replay.Until(reports, now)
	r, err := c.Send(ctx, sofar)
	if err != nil {
		return err
	}
	fmt.Printf("%d tables, %d reports so far: %d accepted, %d late, %d duplicate, %d unknown, %d invalid\n",
		len(list), len(sofar), r.Accepted, r.Late, r.Duplicate, r.Unknown, r.Invalid)
	if !*follow {
		return nil
	}
	for _, rep := range reports[len(sofar):] {
		at := time.UnixMilli(rep.Since.(int64))
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Until(at)):
		}
		if _, err := c.Send(ctx, []tables.Report{rep}); err != nil {
			fmt.Fprintln(os.Stderr, "tablereplay:", err)
			continue
		}
		fmt.Printf("%s %s %s %s\n", at.Format("15:04"), rep.Plant, rep.Table, tables.StateLabel[rep.State])
	}
	return nil
}
