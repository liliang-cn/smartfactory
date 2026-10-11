package web

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	paho "github.com/eclipse/paho.mqtt.golang"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"smartfactory/internal/adapters"
	"smartfactory/internal/audit"
	"smartfactory/internal/auth"
	"smartfactory/internal/config"
	"smartfactory/internal/engine"
	"smartfactory/internal/erp"
	"smartfactory/internal/guard"
	"smartfactory/internal/mcptools"
	"smartfactory/internal/sfptest"
	"smartfactory/internal/tags"
)

// A press fed by a real device (a client of the plant's own broker): a person's command passes
// the rules; an AI's waits for an operator at the advisory level, and reaches the device only
// once approved — never when denied or when it lapses; the guarded and autonomous levels let
// more through; commands for simulated machines go on as before. Every one is in the activity
// log with the press's state.
func TestGuard(t *testing.T) {
	if _, err := os.Stat("../engine/js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	std := sfptest.Load(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng, err := engine.New(log)
	if err != nil {
		t.Fatal(err)
	}
	eng.Dir = t.TempDir()
	eng.Single()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); <-eng.Done })
	go eng.Run(ctx)
	trail, _ := audit.Open(t.TempDir(), log)
	t.Cleanup(func() { trail.Close() })
	var cfg map[string]any
	json.Unmarshal([]byte(eng.BuiltinTemplate("huaduan")), &cfg)
	cfg["id"], cfg["name"], cfg["short"] = "jianghe", "江河锻造", "江河"
	b, _ := json.Marshal(cfg)
	if r := eng.CreateCompany(b); !r.OK {
		t.Fatal(r.Errors)
	}

	// the plant's broker and a press on it
	hub := tags.NewHub()
	go func() {
		sub := hub.Subscribe()
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
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close()
	broker, err := adapters.StartBroker(config.Broker{Listen: addr, Prefix: "sf", Users: map[string]string{"press": "pw"}}, hub, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { broker.Close() })
	var mu sync.Mutex
	var got []string
	dev := paho.NewClient(paho.NewClientOptions().AddBroker("tcp://" + addr).SetClientID("press-hp101").SetUsername("press").SetPassword("pw"))
	if tok := dev.Connect(); !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
		t.Fatal("device cannot connect", tok.Error())
	}
	t.Cleanup(func() { dev.Disconnect(100) })
	dev.Subscribe("sf/jianghe/p1/HP-101/cmd", 1, func(_ paho.Client, m paho.Message) {
		var c struct{ Cmd string }
		json.Unmarshal(m.Payload(), &c)
		mu.Lock()
		got = append(got, c.Cmd)
		mu.Unlock()
	}).Wait()
	report := func(field string, v any) {
		b, _ := json.Marshal(v)
		dev.Publish("sf/jianghe/p1/HP-101/"+field, 0, false, b).Wait()
	}
	report("temp", 60)
	received := func() []string { mu.Lock(); defer mu.Unlock(); return append([]string{}, got...) }
	waitFor := func(what string, ok func() bool) {
		t.Helper()
		for deadline := time.Now().Add(10 * time.Second); !ok(); time.Sleep(20 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting: %s", what)
			}
		}
	}
	waitFor("the press counts as real", func() bool { return hub.Real("jianghe", "p1", "HP-101") })
	if hub.Real("jianghe", "p1", "IH-101") {
		t.Fatal("a machine nothing reports from counts as real")
	}

	g, err := guard.Open(t.TempDir(), eng, hub, trail, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { g.Close() })
	now := time.Now()
	g.Now = func() time.Time { return now }

	op, opk := auth.NewToken("张工", auth.Operator, []string{"jianghe"}, "")
	ai, aik := auth.NewToken("Claude", auth.Operator, []string{"jianghe"}, "")
	admin, adk := auth.NewToken("厂长", auth.Admin, nil, "")
	store, _ := erp.Open(t.TempDir())
	t.Cleanup(func() { store.Close() })
	ms := mcptools.New(hub, eng, "test", trail, g)
	srv := &Server{Hub: hub, Engine: eng, Keys: keyring(t, "", opk, aik, adk), Audit: trail, Log: log, Guard: g, Production: erp.New(store, eng, log),
		MCP: mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return ms }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, DisableLocalhostProtection: true})}
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
	call := func(tool string, args map[string]any) map[string]any {
		t.Helper()
		cl := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
		s, err := cl.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", HTTPClient: &http.Client{Transport: bearer{ai}}, DisableStandaloneSSE: true}, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		res, err := s.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		if res.IsError {
			t.Fatalf("%s: %v", tool, res.Content)
		}
		var out map[string]any
		b, _ := json.Marshal(res.StructuredContent)
		json.Unmarshal(b, &out)
		return out
	}
	send := func(asset, cmd string, arg *float64) map[string]any {
		a := map[string]any{"company": "jianghe", "plant": "p1", "asset": asset, "command": cmd}
		if arg != nil {
			a["arg"] = *arg
		}
		return call("send_command", a)
	}
	f := func(x float64) *float64 { return &x }
	quiet := func(why string) {
		t.Helper()
		time.Sleep(300 * time.Millisecond)
		if r := received(); len(r) > 0 {
			t.Fatalf("%s: the press received %v", why, r)
		}
	}

	// a simulated machine: as before, even for an AI at the advisory level
	if out := send("IH-101", "stop", nil); out["status"] != "sent" {
		t.Errorf("an AI's command for a simulated machine: %v", out)
	}

	// advisory: the AI's command waits, the press hears nothing until an operator approves
	out := send("HP-101", "stop", nil)
	if out["status"] != "waiting" || !strings.HasPrefix(fmt.Sprint(out["request"]), "CQ-") {
		t.Fatalf("an AI's command at the advisory level: %v", out)
	}
	id := fmt.Sprint(out["request"])
	quiet("a command waiting for approval")
	_, b = do(op, "GET", "/api/guard/jianghe", nil)
	std.Check("GuardOverview", b)
	_, b = do(op, "GET", "/api/guard/jianghe/requests?status=waiting", nil)
	std.Check("CommandRequests", b)
	if !strings.Contains(string(b), id) || !strings.Contains(string(b), "数据来自 press-hp101") {
		t.Errorf("waiting requests: %s", b)
	}
	if code, b := do(op, "POST", "/api/guard/jianghe/requests/"+id+"/approve", nil); code != 200 {
		t.Fatalf("approve: %d %s", code, b)
	} else {
		std.Check("CommandRequest", b)
	}
	waitFor("the approved command reaches the press", func() bool { return strings.Contains(fmt.Sprint(received()), "stop") })
	if r := call("command_request", map[string]any{"company": "jianghe", "id": id}); r["status"] != "sent" || r["decidedBy"] != "张工" {
		t.Errorf("the AI asks how it went: %v", r)
	}
	if code, _ := do(op, "POST", "/api/guard/jianghe/requests/"+id+"/approve", nil); code != 409 {
		t.Errorf("approved twice: %d", code)
	}
	mu.Lock()
	got = nil
	mu.Unlock()

	// denied: never reaches the press; the AI hears it when it waits for the decision
	id = fmt.Sprint(send("HP-101", "speed", f(1.1))["request"])
	go func() {
		time.Sleep(200 * time.Millisecond)
		do(op, "POST", "/api/guard/jianghe/requests/"+id+"/deny", map[string]string{"reason": "换班时不调速"})
	}()
	if r := call("command_request", map[string]any{"company": "jianghe", "id": id, "wait": 10}); r["status"] != "denied" || r["result"] != "换班时不调速" {
		t.Errorf("denied: %v", r)
	}
	quiet("a denied command")

	// lapsed: past its time it expires and can no longer be approved
	id = fmt.Sprint(send("HP-101", "maint", nil)["request"])
	now = now.Add(16 * time.Minute)
	if code, _ := do(op, "POST", "/api/guard/jianghe/requests/"+id+"/approve", nil); code != 409 {
		t.Errorf("a lapsed command was approved: %d", code)
	}
	if r := call("command_request", map[string]any{"company": "jianghe", "id": id}); r["status"] != "expired" {
		t.Errorf("lapsed: %v", r)
	}
	quiet("a lapsed command")

	// the rules: the press reports a fault, and a person's start is refused
	report("fault", "主电机过载")
	waitFor("the twin sees the fault", func() bool { a, _ := eng.AssetState("jianghe", "p1", "HP-101"); return a.State == "fault" })
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/live?company=jianghe&token="+op, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.SetReadLimit(16 << 20)
	defer c.CloseNow()
	var hello map[string]any
	wsjson.Read(ctx, c, &hello)
	wsjson.Write(ctx, c, map[string]any{"type": "watch", "company": "jianghe", "plant": "p1"})
	wsjson.Write(ctx, c, map[string]any{"type": "cmd", "plant": "p1", "asset": "HP-101", "cmd": "start"})
	rctx, rcancel := context.WithTimeout(ctx, 15*time.Second)
	defer rcancel()
	for {
		var m map[string]any
		if err := wsjson.Read(rctx, c, &m); err != nil {
			t.Fatal("no answer to the start:", err)
		}
		if m["type"] == "result" {
			if m["ok"] != false || !strings.Contains(fmt.Sprint(m["msg"]), "故障") {
				t.Errorf("a person's start on a faulty press: %v", m)
			}
			break
		}
	}
	quiet("a refused start")
	report("fault", "")
	waitFor("the fault clears", func() bool { a, _ := eng.AssetState("jianghe", "p1", "HP-101"); return a.State != "fault" })

	// guarded: what the company lists waits, the rest goes
	s := guard.Defaults()
	s.Level, s.Approve = guard.Guarded, []string{"start"}
	if code, b := do(op, "PUT", "/api/guard/jianghe", s); code != 403 {
		t.Errorf("an operator changed the guard: %d %s", code, b)
	}
	if code, b := do(admin, "PUT", "/api/guard/jianghe", s); code != 200 {
		t.Fatalf("set guarded: %d %s", code, b)
	}
	if out := send("HP-101", "maint", nil); out["status"] != "sent" {
		t.Errorf("guarded, a kind not listed: %v", out)
	}
	waitFor("the press hears maint", func() bool { return strings.Contains(fmt.Sprint(received()), "maint") })
	if out := send("HP-101", "start", nil); out["status"] != "waiting" {
		t.Errorf("guarded, a listed kind: %v", out)
	}

	// autonomous: the rules alone
	s.Level = guard.Autonomous
	do(admin, "PUT", "/api/guard/jianghe", s)
	if out := send("HP-101", "speed", f(1.4)); out["status"] != "refused" || !strings.Contains(fmt.Sprint(out["reason"]), "120%") {
		t.Errorf("autonomous, against the rules: %v", out)
	}
	if out := send("HP-101", "speed", f(1.1)); out["status"] != "sent" {
		t.Errorf("autonomous, within the rules: %v", out)
	}
	waitFor("the press hears speed", func() bool { return strings.Contains(fmt.Sprint(received()), "speed") })
	bad := s
	bad.Rules = append(bad.Rules, guard.Rule{Cmd: "start", When: "pressure > 3", Say: "x"})
	if code, b := do(admin, "PUT", "/api/guard/jianghe", bad); code != 422 || !strings.Contains(string(b), "pressure") {
		t.Errorf("a rule on a field that does not exist: %d %s", code, b)
	}

	// the activity log: every command to the press, with its state and the verdict
	entries, _ := trail.List(t.Context(), "jianghe", 0, 100)
	seen := map[string]audit.Entry{}
	for _, e := range entries {
		if _, ok := seen[e.Action]; !ok {
			seen[e.Action] = e
		}
	}
	for _, a := range []string{"command", "command.pending", "command.approved", "command.denied", "command.expired", "command.refused", "guard.config"} {
		if _, ok := seen[a]; !ok {
			t.Errorf("no %s in the activity log", a)
		}
	}
	if e := seen["command.refused"]; !strings.Contains(e.Context, "状态") || !strings.Contains(e.Context, "规则") {
		t.Errorf("a refusal without its context: %+v", e)
	}
	if e := seen["command.approved"]; e.Who != "张工" || !strings.Contains(e.Context, "Claude 发起，张工 批准") {
		t.Errorf("an approval without who asked and who approved: %+v", e)
	}
}

// An ERP token sends, reads and withdraws its company's orders, and is a visitor everywhere
// else: no commands to machines, no notes or activity log, no console, no MCP.
func TestERPRole(t *testing.T) {
	if _, err := os.Stat("../engine/js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng, err := engine.New(log)
	if err != nil {
		t.Fatal(err)
	}
	eng.Dir = t.TempDir()
	eng.Single()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); <-eng.Done })
	go eng.Run(ctx)
	var cfg map[string]any
	json.Unmarshal([]byte(eng.BuiltinTemplate("huaduan")), &cfg)
	cfg["id"], cfg["name"], cfg["short"] = "jianghe", "江河锻造", "江河"
	b, _ := json.Marshal(cfg)
	eng.CreateCompany(b)
	trail, _ := audit.Open(t.TempDir(), log)
	t.Cleanup(func() { trail.Close() })
	store, _ := erp.Open(t.TempDir())
	t.Cleanup(func() { store.Close() })
	hub := tags.NewHub()
	g, _ := guard.Open(t.TempDir(), eng, hub, trail, log)
	t.Cleanup(func() { g.Close() })
	ms := mcptools.New(hub, eng, "test", trail, g)
	robot, rk := auth.NewToken("ERP", auth.ERP, []string{"jianghe"}, "")
	other, ok := auth.NewToken("别家 ERP", auth.ERP, []string{"tiankong"}, "")
	ts := httptest.NewServer((&Server{Hub: hub, Engine: eng, Keys: keyring(t, "", rk, ok), Audit: trail, Log: log, Guard: g, Production: erp.New(store, eng, log),
		MCP: mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return ms }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, DisableLocalhostProtection: true})}).Handler())
	t.Cleanup(ts.Close)
	do := func(token, method, path string, body any) int {
		t.Helper()
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = strings.NewReader(string(b))
		}
		req, _ := http.NewRequest(method, ts.URL+path, rd)
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if c := do(robot, "POST", "/api/erp/jianghe/orders", map[string]any{"no": "MO-1", "partNo": "LG-B15", "qty": 5}); c != 200 {
		t.Errorf("send an order: %d", c)
	}
	if c := do(robot, "GET", "/api/erp/jianghe/orders", nil); c != 200 {
		t.Errorf("read orders: %d", c)
	}
	if c := do(robot, "DELETE", "/api/erp/jianghe/orders/MO-1", nil); c != 200 {
		t.Errorf("withdraw an order: %d", c)
	}
	if c := do(other, "GET", "/api/erp/jianghe/orders", nil); c != 403 {
		t.Errorf("another company's ERP read the orders: %d", c)
	}
	for _, p := range []struct{ method, path string }{
		{"GET", "/api/admin"}, {"GET", "/api/admin/erp/jianghe"}, {"GET", "/api/audit/jianghe"}, {"GET", "/api/guard/jianghe"},
		{"PATCH", "/api/library/jianghe/tickets/x"}, {"POST", "/api/library/jianghe/docs"},
	} {
		if c := do(robot, p.method, p.path, map[string]any{}); c != 401 && c != 403 && c != 404 && c != 503 {
			t.Errorf("%s %s with an ERP token: %d", p.method, p.path, c)
		}
	}
	cl := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	if s, err := cl.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", HTTPClient: &http.Client{Transport: bearer{robot}}, DisableStandaloneSSE: true}, nil); err == nil {
		s.Close()
		t.Error("an ERP token got into MCP")
	}
	// on the live link it is a visitor: its commands never reach real equipment
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/live?company=jianghe&token="+robot, nil)
	if err != nil {
		t.Fatal(err)
	}
	c.SetReadLimit(16 << 20)
	defer c.CloseNow()
	var hello map[string]any
	wsjson.Read(ctx, c, &hello)
	if hello["operator"] != false || hello["role"] != "visitor" {
		t.Errorf("the live link took an ERP token for %v / %v", hello["operator"], hello["role"])
	}
}
