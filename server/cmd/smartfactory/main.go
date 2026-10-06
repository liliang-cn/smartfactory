// smartfactory serves the digital twin, its protocol adapters and its MCP endpoint, or with
// `edge` runs a protocol gateway in front of real measurements.
//
//	smartfactory [-config smartfactory.json] [-http :4719]
//	smartfactory edge -config edge.json
//	smartfactory version
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"smartfactory/internal/adapters"
	"smartfactory/internal/config"
	"smartfactory/internal/edge"
	"smartfactory/internal/mcptools"
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
	cmd := "serve"
	if len(args) > 0 && (args[0] == "serve" || args[0] == "edge" || args[0] == "version") {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "version":
		fmt.Println(version)
	case "edge":
		err = runEdge(ctx, args, log)
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
	if cfg.MQTT.Listen != "" {
		b, err := adapters.StartBroker(cfg.MQTT, hub, log)
		if err != nil {
			return fmt.Errorf("mqtt broker: %w", err)
		}
		defer b.Close()
	}
	for _, src := range cfg.Sources {
		switch src.Type {
		case "mqtt":
			go adapters.RunMQTT(ctx, src, hub, log)
		case "modbus":
			go adapters.RunModbus(ctx, src, hub, log)
		case "opcua":
			go adapters.RunOPCUA(ctx, src, hub, log)
		}
	}

	ms := mcptools.New(hub, version)
	ws := &sfweb.Server{
		Hub: hub, Token: cfg.Token, Version: version, Static: web.Dist(), Log: log,
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
	if cfg.Token != "" {
		mcpState = "enabled at /mcp (Bearer token)"
	}
	log.Info("smartfactory serving", "version", version, "http", cfg.HTTP, "sources", len(cfg.Sources), "mcp", mcpState)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
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
