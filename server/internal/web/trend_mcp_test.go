package web

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"smartfactory/internal/audit"
	"smartfactory/internal/auth"
	"smartfactory/internal/engine"
	"smartfactory/internal/mcptools"
	"smartfactory/internal/series"
	"smartfactory/internal/tags"
)

// machine_trend over MCP: a member of the company gets a machine's stops with why and the
// ranking; a token for another company gets nothing.
func TestMachineTrendMCP(t *testing.T) {
	if _, err := os.Stat("../engine/js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng, err := engine.New(log)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); <-eng.Done })
	go eng.Run(ctx)
	trail, _ := audit.Open(t.TempDir(), log)
	t.Cleanup(func() { trail.Close() })
	rd, err := series.Open(t.TempDir(), series.Retention{Raw: 14 * 24 * time.Hour, Minute: 90 * 24 * time.Hour, Hour: 365 * 24 * time.Hour}, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { rd.Close() })
	start := time.Now().Add(-2 * time.Hour)
	put := func(m int, field string, v any) {
		rd.Add(tags.Tag{Company: "huaduan", Plant: "p1", Asset: "HP-101", Field: field, Value: v, Source: "plc", At: start.Add(time.Duration(m) * time.Minute)})
	}
	for m := 0; m < 120; m++ {
		put(m, "temp", 60.0)
	}
	put(0, "state", "running")
	put(30, "state", "fault")
	put(30, "fault", "离合器制动异常")
	put(40, "state", "running")
	put(40, "fault", "")
	if err := rd.Flush(); err != nil {
		t.Fatal(err)
	}

	member, mk := auth.NewToken("工艺员", auth.Member, []string{"huaduan"}, "")
	stranger, sk := auth.NewToken("别家", auth.Member, []string{"hetian"}, "")
	ms := mcptools.New(tags.NewHub(), eng, "test", trail, nil)
	mcptools.AddTrend(ms, rd)
	srv := &Server{Hub: tags.NewHub(), Engine: eng, Keys: keyring(t, "", mk, sk), Audit: trail, Log: log, Series: rd,
		MCP: mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return ms }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, DisableLocalhostProtection: true})}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	call := func(token string, a map[string]any) (*mcp.CallToolResult, string) {
		t.Helper()
		cl := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
		sess, err := cl.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", HTTPClient: &http.Client{Transport: bearer{token}}, DisableStandaloneSSE: true}, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer sess.Close()
		res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "machine_trend", Arguments: a})
		if err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(res.StructuredContent)
		if res.IsError {
			for _, c := range res.Content {
				if tc, ok := c.(*mcp.TextContent); ok {
					b = append(b, tc.Text...)
				}
			}
		}
		return res, string(b)
	}

	res, out := call(member, map[string]any{"company": "huaduan", "asset": "HP-101", "hours": 3})
	if res.IsError || !strings.Contains(out, `"reason":"离合器制动异常"`) || !strings.Contains(out, `"stop_minutes":10`) || !strings.Contains(out, `"field":"temp"`) {
		t.Errorf("one machine: %s", out)
	}
	res, out = call(member, map[string]any{"company": "huaduan"})
	if res.IsError || !strings.Contains(out, `"asset":"HP-101"`) || !strings.Contains(out, `"stop_count":1`) {
		t.Errorf("ranking: %s", out)
	}
	if res, out = call(stranger, map[string]any{"company": "huaduan", "asset": "HP-101"}); !res.IsError || strings.Contains(out, "离合器") {
		t.Errorf("another company's token read the trend: %s", out)
	}
	if res, out = call(member, map[string]any{"company": "huaduan", "asset": "RF-101"}); !res.IsError || !strings.Contains(out, "simulated") {
		t.Errorf("a simulated machine: %s", out)
	}
}
