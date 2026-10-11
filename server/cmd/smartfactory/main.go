// smartfactory serves the digital twin, its protocol adapters and its MCP endpoint, or with
// `edge` runs a protocol gateway in front of real measurements.
//
//	smartfactory [-config smartfactory.json] [-http :4719]
//	smartfactory edge -config edge.json
//	smartfactory devices -config devices.json
//	smartfactory token -name 张工 -role operator -company huaduan
//	smartfactory version
//
// and, against a running server, the console's commands (cli.go): login, customers, industries,
// tokens, kb, activity, agent.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"smartfactory/internal/adapters"
	"smartfactory/internal/agents"
	"smartfactory/internal/audit"
	"smartfactory/internal/auth"
	"smartfactory/internal/config"
	"smartfactory/internal/devices"
	"smartfactory/internal/edge"
	"smartfactory/internal/engine"
	"smartfactory/internal/history"
	"smartfactory/internal/knowledge"
	"smartfactory/internal/mcptools"
	"smartfactory/internal/sfconv"
	"smartfactory/internal/tags"
	sfweb "smartfactory/internal/web"
	"smartfactory/web"
)

var version = "dev"

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	args := os.Args[1:]
	// the command line for administrators, against a running server
	if len(args) > 0 {
		if run, ok := cliCommands[args[0]]; ok {
			if err := run(ctx, args[1:]); err != nil {
				fmt.Fprintln(os.Stderr, red(err.Error()))
				os.Exit(1)
			}
			return
		}
	}
	cmd := "serve"
	if len(args) > 0 && (args[0] == "serve" || args[0] == "edge" || args[0] == "devices" || args[0] == "history" || args[0] == "token" || args[0] == "version") {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "version":
		fmt.Println(version)
	case "edge":
		err = runEdge(ctx, args, log)
	case "devices":
		err = runDevices(ctx, args, log)
	case "history":
		err = runHistory(ctx, args, log)
	case "token":
		err = runToken(args)
	default:
		err = serve(ctx, args, log)
	}
	if err != nil {
		log.Error(err.Error())
		os.Exit(1)
	}
}

func serve(ctx context.Context, args []string, log *slog.Logger) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	path := fs.String("config", "", "config file (JSON); optional")
	addr := fs.String("http", "", "HTTP listen address, overrides the config")
	fs.Parse(args)

	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	if *addr != "" {
		cfg.HTTP = *addr
	}

	hub := tags.NewHub()
	eng, err := engine.New(log)
	if err != nil {
		return err
	}
	eng.Dir = cfg.Data
	eng.LoadConfigs()
	eng.Restore()
	go eng.Run(ctx)
	for _, h := range cfg.History {
		go history.Record(ctx, eng, h.Company, h.DSN, log)
	}
	// Values read from equipment feed the server-side simulation.
	go func() {
		sub := hub.Subscribe()
		defer hub.Unsubscribe(sub)
		for {
			select {
			case <-ctx.Done():
				return
			case e := <-sub.C:
				if e.Type == "tag" {
					eng.Ingest(e.Tag.Company, e.Tag.Plant, e.Tag.Asset, e.Tag.Field, e.Tag.Value, e.Tag.Source)
				}
			}
		}
	}()
	if cfg.MQTT.Listen != "" {
		b, err := adapters.StartBroker(cfg.MQTT, hub, log)
		if err != nil {
			return fmt.Errorf("mqtt broker: %w", err)
		}
		defer b.Close()
	}
	for _, src := range cfg.Sources {
		if src.Convention == "sf" && (src.Type == "modbus" || src.Type == "opcua") {
			ms := eng.Machines(src.Company, src.Plant)
			if len(ms) == 0 {
				log.Error("source skipped: no such plant", "source", src.Name, "plant", src.Company+"/"+src.Plant)
				continue
			}
			src = sfconv.Expand(src, ms, eng.Fleet(src.Company, src.Plant))
		}
		switch src.Type {
		case "mqtt":
			go adapters.RunMQTT(ctx, src, hub, log)
		case "modbus":
			go adapters.RunModbus(ctx, src, hub, log)
		case "opcua":
			go adapters.RunOPCUA(ctx, src, hub, log)
		}
	}

	advisors := map[string]string{}
	advisorAPI := map[string]sfweb.Advisor{}
	for _, h := range cfg.History {
		if h.Advisor != "" {
			advisors[h.Company] = h.Advisor
		}
		if h.AdvisorAPI != "" {
			advisorAPI[h.Company] = sfweb.Advisor{API: strings.TrimRight(h.AdvisorAPI, "/"), Token: h.AdvisorToken, Approver: h.ApproverToken}
		}
	}
	var assistants *agents.Runner
	if cfg.Data != "" {
		assistants, err = agents.New(filepath.Join(cfg.Data, "agents"), eng, log)
		if err != nil {
			return fmt.Errorf("assistants: %w", err)
		}
		defer assistants.Close()
		if !assistants.Ready() {
			log.Warn("assistants cannot answer: set LLM_BASE_URL, LLM_API_KEY and LLM_MODEL")
		}
	}
	keys, err := auth.NewKeyring(cfg.Token, cfg.Tokens)
	if err != nil {
		return err
	}
	var trail *audit.Log
	if cfg.Data != "" {
		if trail, err = audit.Open(cfg.Data, log); err != nil {
			return fmt.Errorf("audit log: %w", err)
		}
		defer trail.Close()
		// tokens issued in the console (/admin) live here, next to the config file's
		accounts, err := auth.OpenStore(cfg.Data)
		if err != nil {
			return fmt.Errorf("accounts: %w", err)
		}
		defer accounts.Close()
		if err := keys.Attach(accounts); err != nil {
			return fmt.Errorf("accounts: %w", err)
		}
	}
	if assistants != nil {
		assistants.Audit = trail
	}
	// each company's knowledge base: its process as a graph, its documents linked to it
	var kb *knowledge.Base
	if cfg.Data != "" {
		if kb, err = knowledge.Open(filepath.Join(cfg.Data, "knowledge"), eng, log); err != nil {
			return fmt.Errorf("knowledge: %w", err)
		}
		defer kb.Close()
		if assistants != nil {
			assistants.KB = kb
		}
		eng.OnChange = func(company string, deleted bool) {
			if deleted {
				kb.Drop(company)
				return
			}
			if err := kb.SyncProcess(ctx, company); err != nil {
				log.Error("knowledge: process", "company", company, "err", err)
			}
		}
		go func() {
			for _, c := range eng.CompanyIDs() {
				if err := kb.SyncProcess(ctx, c); err != nil {
					log.Error("knowledge: process", "company", c, "err", err)
				}
				if assistants != nil {
					if n, err := assistants.IndexDocs(ctx, c, false); err != nil {
						log.Error("knowledge: documents", "company", c, "err", err)
					} else if n > 0 {
						log.Info("knowledge: documents indexed", "company", c, "docs", n)
					}
				}
			}
		}()
	}
	ms := mcptools.New(hub, eng, version, trail)
	ws := &sfweb.Server{
		Hub: hub, Engine: eng, Keys: keys, Audit: trail, Knowledge: kb, Version: version, Static: web.Dist(), Log: log, Advisors: advisors, AdvisorAPI: advisorAPI, Prefix: cfg.MQTT.Prefix, Agents: assistants,
		// The server usually listens on loopback behind a reverse proxy, which the SDK's DNS
		// rebinding check mistakes for a local-only server; /mcp requires the bearer token anyway.
		MCP: mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return ms }, &mcp.StreamableHTTPOptions{
			Stateless: true, JSONResponse: true, DisableLocalhostProtection: true,
		}),
	}
	srv := &http.Server{Addr: cfg.HTTP, Handler: ws.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	mcpState := "disabled (no token)"
	if !keys.Empty() {
		mcpState = "enabled at /mcp (Bearer token)"
	}
	log.Info("smartfactory serving", "version", version, "http", cfg.HTTP, "companies", eng.CompanyIDs(), "sources", len(cfg.Sources), "mcp", mcpState)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	// Let the engine write its final state before the process goes away.
	select {
	case <-eng.Done:
	case <-time.After(10 * time.Second):
		log.Warn("engine did not finish saving in time")
	}
	return nil
}

// runToken makes a token: the token itself, shown once, and the entry that goes under "tokens"
// in the server config.
func runToken(args []string) error {
	fs := flag.NewFlagSet("token", flag.ExitOnError)
	name := fs.String("name", "", "who holds it, as the audit log shows it")
	role := fs.String("role", "member", "member | operator | admin")
	companies := fs.String("company", "*", "companies it covers, comma-separated; * for every company")
	expires := fs.String("expires", "", "last day it works, YYYY-MM-DD (optional)")
	fs.Parse(args)
	if strings.TrimSpace(*name) == "" {
		return errors.New("token: -name is required")
	}
	r, err := auth.ParseRole(*role)
	if err != nil {
		return err
	}
	var list []string
	for _, c := range strings.Split(*companies, ",") {
		if c = strings.TrimSpace(c); c != "" && c != "*" {
			list = append(list, c)
		}
	}
	token, entry := auth.NewToken(strings.TrimSpace(*name), r, list, *expires)
	if _, err := auth.NewKeyring("", []config.Key{entry}); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(entry, "", "  ")
	fmt.Printf("token (give it to %s; it is not kept anywhere):\n\n  %s\n\nadd to \"tokens\" in the server config:\n\n%s\n", entry.Name, token, b)
	return nil
}

func runEdge(ctx context.Context, args []string, log *slog.Logger) error {
	fs := flag.NewFlagSet("edge", flag.ExitOnError)
	path := fs.String("config", "edge.json", "edge gateway config (JSON)")
	fs.Parse(args)
	cfg, err := edge.Load(*path)
	if err != nil {
		return err
	}
	log.Info("smartfactory edge starting", "version", version, "scrape", cfg.Scrape, "metrics", len(cfg.Metrics))
	return edge.Run(ctx, cfg, log)
}

func runDevices(ctx context.Context, args []string, log *slog.Logger) error {
	fs := flag.NewFlagSet("devices", flag.ExitOnError)
	path := fs.String("config", "devices.json", "emulated equipment config (JSON)")
	fs.Parse(args)
	cfg, err := devices.Load(*path)
	if err != nil {
		return err
	}
	eng, err := engine.New(log)
	if err != nil {
		return err
	}
	log.Info("smartfactory devices starting", "version", version, "plants", len(cfg.Plants))
	return devices.Run(ctx, cfg, eng, log)
}

// runHistory: `history model` prints the advisor's semantic model of the history database;
// `history backfill` fills a company's database with what its plants would have done over the
// past days, simulated offline, ending where live recording takes over.
func runHistory(ctx context.Context, args []string, log *slog.Logger) error {
	if len(args) == 0 || (args[0] != "model" && args[0] != "backfill" && args[0] != "load") {
		return errors.New("usage: smartfactory history model | history backfill|load -config file -company id [-days 30] [-warmup 3] [-reset]")
	}
	if args[0] == "model" {
		_, err := os.Stdout.Write(history.Model)
		return err
	}
	fs := flag.NewFlagSet("history "+args[0], flag.ExitOnError)
	path := fs.String("config", "", "server config with the company's history database")
	company := fs.String("company", "", "company id")
	days := fs.Int("days", 30, "days of history to simulate")
	warmup := fs.Float64("warmup", 3, "days simulated first and not recorded, so the plants are in a steady state")
	reset := fs.Bool("reset", false, "empty the database first")
	dt := fs.Float64("dt", 1, "simulation step, seconds")
	fs.Parse(args[1:])
	cfg, err := config.Load(*path)
	if err != nil {
		return err
	}
	dsn := ""
	for _, h := range cfg.History {
		if h.Company == *company {
			dsn = h.DSN
		}
	}
	if dsn == "" {
		return fmt.Errorf("no history database for company %q in %s", *company, *path)
	}
	st, err := history.Open(ctx, dsn)
	if err != nil {
		return err
	}
	defer st.Close()
	if *reset {
		if err := st.Reset(ctx); err != nil {
			return err
		}
	}
	if args[0] == "load" {
		return loadHistory(ctx, st, log)
	}
	eng, err := engine.NewOffline(log)
	if err != nil {
		return err
	}
	eng.Dir = cfg.Data
	eng.LoadConfigs()
	if err := st.WriteDims(ctx, eng.HistoryDims(*company)); err != nil {
		return err
	}
	end := time.Now().Truncate(time.Hour)
	start := end.Add(-time.Duration(*days) * 24 * time.Hour)
	log.Info("history backfill", "company", *company, "from", start.Format("2006-01-02 15:04"), "to", end.Format("2006-01-02 15:04"), "warmup days", *warmup)
	if !eng.Advance(*company, *warmup*86400, *dt) {
		return fmt.Errorf("no company %q", *company)
	}
	eng.HistoryOn(*company, start)
	t0 := time.Now()
	for h := 0; h < *days*24; h++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		eng.Advance(*company, 3600, *dt)
		if _, err := st.Write(ctx, eng.History(*company, false), start.Add(time.Duration(h+1)*time.Hour)); err != nil {
			return err
		}
		if (h+1)%24 == 0 {
			log.Info("history backfill", "day", (h+1)/24, "of", *days, "elapsed", time.Since(t0).Round(time.Second))
		}
	}
	log.Info("history backfill done", "db", st.Summary(ctx))
	return nil
}

// loadHistory stores what scripts/backfill.mjs prints: the dims, then one line per simulated hour.
func loadHistory(ctx context.Context, st *history.Store, log *slog.Logger) error {
	in := bufio.NewReader(os.Stdin)
	n := 0
	for {
		line, err := in.ReadBytes('\n')
		if len(line) > 0 {
			var rec struct {
				Dims    json.RawMessage `json:"dims"`
				At      int64           `json:"at"`
				History json.RawMessage `json:"history"`
			}
			if err := json.Unmarshal(line, &rec); err != nil {
				return fmt.Errorf("line %d: %w", n+1, err)
			}
			if rec.Dims != nil {
				if err := st.WriteDims(ctx, string(rec.Dims)); err != nil {
					return err
				}
			} else if _, err := st.Write(ctx, string(rec.History), time.UnixMilli(rec.At)); err != nil {
				return err
			}
			n++
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	log.Info("history loaded", "lines", n, "db", st.Summary(ctx))
	return nil
}
