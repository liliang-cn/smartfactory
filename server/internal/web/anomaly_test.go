package web

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"smartfactory/internal/anomaly"
	"smartfactory/internal/audit"
	"smartfactory/internal/auth"
	"smartfactory/internal/config"
	"smartfactory/internal/engine"
	"smartfactory/internal/mcptools"
	"smartfactory/internal/series"
	"smartfactory/internal/sfptest"
	"smartfactory/internal/tags"
)

// Readings unusual for their machine, end to end: a press's readings through the hub (checked by
// the validator, learned by the detector), its hint in the company's list for members, its fields
// for anyone watching the twin and on the live link, the hint for an AI over MCP, the press learned
// again by an administrator, and nothing for the machines the engine simulates. Every answer holds
// to the protocol.
func TestAnomalies(t *testing.T) {
	if _, err := os.Stat("../engine/js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	std := sfptest.Load(t)
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
	st, err := anomaly.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	set := anomaly.SettingsFrom(config.Anomaly{Warmup: config.Duration{Duration: 30 * time.Minute}})
	hints, err := anomaly.New(st, set, trail, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { hints.Close() })

	// readings an hour back to now, once a second: the detector's clock follows them
	clock := time.Now().Add(-time.Hour)
	hints.Now = func() time.Time { return clock }
	hub := tags.NewHub()
	hub.Source("plc", "modbus", "tcp://127.0.0.1:41502", 4)
	hub.Check = (&series.Validator{Machines: eng.Machines}).Check
	hub.Tap = hints.Add
	rnd := rand.New(rand.NewPCG(3, 4))
	publish := func(field string, v any) {
		hub.Publish(tags.Tag{Company: "huaduan", Plant: "p1", Asset: "HP-101", Field: field, Value: v, Source: "plc", At: clock})
	}
	run := func(d time.Duration, temp float64) {
		for i := 0; i < int(d/time.Second); i++ {
			publish("temp", temp+rnd.NormFloat64()*0.5)
			publish("power", 40+rnd.NormFloat64())
			clock = clock.Add(time.Second)
		}
	}
	publish("state", "running")
	run(40*time.Minute, 60)
	// a reading no press can give is flagged by the validator, and neither learned nor judged
	publish("temp", 5000.0)
	run(2*time.Minute, 64)

	admin, ak := auth.NewToken("厂长", auth.Admin, nil, "")
	member, mk := auth.NewToken("工艺员", auth.Member, []string{"huaduan"}, "")
	ms := mcptools.New(hub, eng, "test", trail, nil)
	srv := &Server{Hub: hub, Engine: eng, Keys: keyring(t, "", ak, mk), Audit: trail, Log: log, Hints: hints,
		MCP: mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return ms }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, DisableLocalhostProtection: true})}
	o := srv.Operations()
	mcptools.AddHints(ms, func(w auth.Who, c string) (*anomaly.Report, error) {
		r, err := o.Anomalies(w, c)
		if err != nil {
			return nil, err
		}
		return &r.Report, nil
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	do := func(token, method, path string, body any) (int, []byte) {
		t.Helper()
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = strings.NewReader(string(b))
		}
		req, _ := http.NewRequest(method, ts.URL+path, rd)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, b
	}

	// the company's list: members and above
	if got, _ := do("", "GET", "/api/anomalies/huaduan", nil); got != 401 {
		t.Errorf("a visitor read the company's hints: %d", got)
	}
	got, b := do(member, "GET", "/api/anomalies/huaduan", nil)
	if got != 200 {
		t.Fatalf("hints: %d %s", got, b)
	}
	std.Check("AnomalyReport", b)
	var rep anomaly.Report
	json.Unmarshal(b, &rep)
	if len(rep.Current) != 1 || rep.Current[0].Asset != "HP-101" || rep.Current[0].Field != "temp" || rep.Current[0].Kind != anomaly.Level ||
		rep.Current[0].State != "running" || rep.Current[0].Z < 4 || math.Abs(rep.Current[0].Mean-60) > 0.2 {
		t.Fatalf("hints now: %+v", rep.Current)
	}
	if len(rep.Machines) != 1 {
		t.Errorf("machines: %d, only HP-101 sent readings", len(rep.Machines))
	}

	// one machine's fields: anyone watching
	got, b = do("", "GET", "/api/anomalies/huaduan/p1/HP-101", nil)
	if got != 200 {
		t.Fatalf("machine: %d %s", got, b)
	}
	std.Check("AnomalyMachine", b)
	var m anomaly.Machine
	json.Unmarshal(b, &m)
	if len(m.Fields) != 2 || m.Fields[0].Field != "temp" || m.Fields[0].Anomaly == nil || m.Fields[0].Now == nil || !m.Fields[0].Now.Warm ||
		m.Fields[1].Field != "power" || m.Fields[1].Anomaly != nil || math.Abs(m.Fields[1].Z) > 4 {
		t.Errorf("HP-101: %s", b)
	}
	// the 5000 °C reading was flagged and not learned
	if n := m.Fields[0].Now.N; n != 42*60 {
		t.Errorf("learned %d temperatures, want %d", n, 42*60)
	}
	// the engine simulates every other press: none has readings, none has a hint
	if got, _ := do("", "GET", "/api/anomalies/huaduan/p1/HP-102", nil); got != 404 {
		t.Errorf("a simulated press: %d", got)
	}

	// on the live link, with the sources
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/live?company=huaduan", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	ws.SetReadLimit(16 << 20)
	ws.Write(ctx, websocket.MessageText, []byte(`{"type":"watch","company":"huaduan","plant":"p1"}`))
	deadline, stop := context.WithTimeout(ctx, 10*time.Second)
	defer stop()
	for {
		_, b, err := ws.Read(deadline)
		if err != nil {
			t.Fatalf("no hints on the live link: %v", err)
		}
		var msg struct {
			Type      string            `json:"type"`
			Anomalies []anomaly.Anomaly `json:"anomalies"`
		}
		json.Unmarshal(b, &msg)
		if msg.Type != "anomalies" {
			continue
		}
		std.Check("ServerMessage", b)
		if len(msg.Anomalies) != 1 || msg.Anomalies[0].Asset != "HP-101" {
			t.Errorf("live hints: %s", b)
		}
		break
	}

	// for an AI over MCP
	cl := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	sess, err := cl.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", HTTPClient: &http.Client{Transport: bearer{member}}, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "list_anomalies", Arguments: map[string]any{"company": "huaduan", "asset": "HP-101"}})
	sess.Close()
	if err != nil || res.IsError {
		t.Fatalf("list_anomalies: %v %v", err, res)
	}
	out, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(out), `"kind":"level"`) || !strings.Contains(string(out), `"fields"`) {
		t.Errorf("list_anomalies: %s", out)
	}

	// learning the press again: administrators
	if got, _ := do(member, "POST", "/api/admin/anomalies/huaduan/reset", map[string]any{"machines": []string{"HP-101"}}); got != 403 {
		t.Errorf("a member reset a press: %d", got)
	}
	got, b = do(admin, "POST", "/api/admin/anomalies/huaduan/reset", map[string]any{"machines": []string{"HP-101", "HP-102"}})
	if got != 200 {
		t.Fatalf("reset: %d %s", got, b)
	}
	std.Check("AnomalyResetResult", b)
	var outcomes []map[string]any
	json.Unmarshal(b, &outcomes)
	if len(outcomes) != 2 || outcomes[0]["ok"] != true || outcomes[1]["ok"] == true {
		t.Errorf("reset: %s", b)
	}
	if got, _ := do("", "GET", "/api/anomalies/huaduan/p1/HP-101", nil); got != 404 {
		t.Errorf("after a reset the press still has normals: %d", got)
	}
	// and as a console action, confirmed
	run(time.Minute, 60)
	got, b = do(admin, "POST", "/api/admin/actions", map[string]any{"kind": "reset_baseline", "args": map[string]any{"company": "huaduan", "machines": []string{"HP-101"}}})
	if got != 200 || !strings.Contains(string(b), `"ok": true`) {
		t.Fatalf("action: %d %s", got, b)
	}

	hints.Flush()
	entries, _ := trail.List(t.Context(), "huaduan", 0, 50)
	var actions []string
	for _, e := range entries {
		actions = append(actions, e.Action)
	}
	std.Check("AuditLog", mustJSON(entries))
	for _, want := range []string{"anomaly", "anomaly.reset"} {
		if !strings.Contains(","+strings.Join(actions, ",")+",", ","+want+",") {
			t.Errorf("not in the activity log: %s (have %v)", want, actions)
		}
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
