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
	"testing/fstest"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"smartfactory/internal/agents"
	"smartfactory/internal/audit"
	"smartfactory/internal/auth"
	"smartfactory/internal/engine"
	"smartfactory/internal/mcptools"
	"smartfactory/internal/sfptest"
	"smartfactory/internal/tags"
)

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

// Each role can do what it should on its own companies, nothing more, by every way in — the
// page, the live link and MCP — and whatever changes is in the audit log under who did it.
func TestRoles(t *testing.T) {
	if _, err := os.Stat("../engine/js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	t.Setenv("LLM_API_KEY", "")
	t.Setenv("AGENTGO_HOME", t.TempDir())
	std := sfptest.Load(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng, err := engine.New(log)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go eng.Run(ctx)
	ag, err := agents.New(t.TempDir(), eng, log)
	if err != nil {
		t.Fatal(err)
	}
	defer ag.Close()
	trail, err := audit.Open(t.TempDir(), log)
	if err != nil {
		t.Fatal(err)
	}
	defer trail.Close()
	ag.Audit = trail

	member, mk := auth.NewToken("小陈", auth.Member, []string{"huaduan"}, "")
	operator, ok := auth.NewToken("张工", auth.Operator, []string{"huaduan"}, "")
	outsider, xk := auth.NewToken("天控的人", auth.Admin, []string{"tiankong"}, "")
	admin, ak := auth.NewToken("李工", auth.Admin, nil, "")
	hub := tags.NewHub()
	ms := mcptools.New(hub, eng, "test", trail)
	srv := &Server{Hub: hub, Engine: eng, Keys: keyring(t, "", mk, ok, xk, ak), Audit: trail, Log: log, Agents: ag,
		Static: fstest.MapFS{"index.html": {Data: []byte("<!doctype html>")}},
		MCP: mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return ms }, &mcp.StreamableHTTPOptions{
			Stateless: true, JSONResponse: true, DisableLocalhostProtection: true,
		})}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	status := func(token, method, path, body string) (int, []byte) {
		t.Helper()
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
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

	// --- the page ---
	var list struct {
		Role string `json:"role"`
		You  string `json:"you"`
	}
	for token, want := range map[string]string{"": "visitor", member: "member", operator: "operator", outsider: "visitor", admin: "admin"} {
		_, b := status(token, "GET", "/api/agents/huaduan", "")
		json.Unmarshal(b, &list)
		std.Check("Assistants", b)
		if list.Role != want {
			t.Errorf("role in huaduan = %q, want %q", list.Role, want)
		}
	}
	var tickets []struct{ ID string }
	_, b := status("", "GET", "/api/library/huaduan/tickets", "")
	json.Unmarshal(b, &tickets)
	patch := `{"status":"处理中"}`
	for token, want := range map[string]int{"": 403, outsider: 403, member: 200, admin: 200} {
		if got, b := status(token, "PATCH", "/api/library/huaduan/tickets/"+tickets[0].ID, patch); got != want {
			t.Errorf("ticket status: %d, want %d: %s", got, want, b)
		}
	}
	doc := `{"title":"试验","body":"# 一\n\n内容"}`
	for token, want := range map[string]int{member: 403, operator: 403, outsider: 403, admin: 200} {
		if got, b := status(token, "POST", "/api/library/huaduan/docs", doc); got != want {
			t.Errorf("document upload: %d, want %d: %s", got, want, b)
		}
	}
	for token, want := range map[string]int{"": 403, member: 403, outsider: 403, operator: 200} {
		if got, _ := status(token, "GET", "/api/audit/huaduan", ""); got != want {
			t.Errorf("audit log: %d, want %d", got, want)
		}
	}

	// --- the live link: an operator's command carries their name; a member's is a visitor's ---
	live := func(token string) (*websocket.Conn, map[string]any) {
		t.Helper()
		c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/live?company=huaduan&token="+token, nil)
		if err != nil {
			t.Fatal(err)
		}
		c.SetReadLimit(16 << 20)
		var hello map[string]any
		if err := wsjson.Read(ctx, c, &hello); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(hello)
		std.Check("ServerMessage", b)
		wsjson.Write(ctx, c, map[string]any{"type": "watch", "company": "huaduan", "plant": "p1"})
		return c, hello
	}
	sub := hub.Subscribe()
	defer hub.Unsubscribe(sub)
	for _, c := range []struct {
		token, role, by, name string
		op                    bool
	}{{member, "member", "visitor", "", false}, {operator, "operator", "operator", "张工", true}} {
		ws, hello := live(c.token)
		if hello["role"] != c.role || hello["operator"] != c.op {
			t.Errorf("hello for %s: role %v operator %v", c.role, hello["role"], hello["operator"])
		}
		wsjson.Write(ctx, ws, map[string]any{"type": "cmd", "plant": "p1", "asset": "HP-101", "cmd": "speed", "arg": 1.1})
		deadline := time.After(5 * time.Second)
	wait:
		for {
			select {
			case e := <-sub.C:
				if e.Type == "cmd" {
					if e.Cmd.By != c.by || e.Cmd.Name != c.name {
						t.Errorf("%s's command went out by %q name %q", c.role, e.Cmd.By, e.Cmd.Name)
					}
					break wait
				}
			case <-deadline:
				t.Fatalf("%s's command never went out", c.role)
			}
		}
		ws.CloseNow()
	}

	// --- MCP ---
	if got, _ := status("", "POST", "/mcp", `{}`); got != 401 {
		t.Errorf("MCP without a token: %d", got)
	}
	call := func(token, tool string, args map[string]any) (*mcp.CallToolResult, error) {
		t.Helper()
		cl := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
		s, err := cl.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", HTTPClient: &http.Client{Transport: bearer{token}}, DisableStandaloneSSE: true}, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		return s.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
	}
	refused := func(res *mcp.CallToolResult, err error) bool { return err != nil || res.IsError }
	res, err := call(member, "list_sites", nil)
	if refused(res, err) {
		t.Fatalf("list_sites: %v %v", err, res)
	}
	sites, _ := json.Marshal(res.StructuredContent)
	if !strings.Contains(string(sites), `"huaduan"`) || strings.Contains(string(sites), `"tiankong"`) {
		t.Errorf("a huaduan token sees: %s", sites)
	}
	stop := map[string]any{"company": "huaduan", "plant": "p1", "asset": "HP-101", "command": "stop"}
	if !refused(call(member, "send_command", stop)) {
		t.Error("a member sent a command over MCP")
	}
	if !refused(call(outsider, "send_command", stop)) {
		t.Error("another company's admin sent a command over MCP")
	}
	if refused(call(operator, "send_command", stop)) {
		t.Error("an operator of huaduan could not send a command")
	}
	if !refused(call(operator, "plant_status", map[string]any{"company": "tiankong", "plant": "p1"})) {
		t.Error("a huaduan token read tiankong")
	}
	if !refused(call(outsider, "create_industry", map[string]any{"config": map[string]any{"id": "x"}})) {
		t.Error("an admin of one company changed what every company shares")
	}

	// --- the audit log: who did what, by which way in ---
	_, b = status(admin, "GET", "/api/audit/huaduan", "")
	std.Check("AuditLog", b)
	var entries []audit.Entry
	json.Unmarshal(b, &entries)
	seen := map[string]bool{}
	for _, e := range entries {
		seen[e.Who+" "+e.Via+" "+e.Action] = true
	}
	for _, want := range []string{"小陈 page ticket.status", "李工 page ticket.status", "李工 page doc.add", "张工 live command", "张工 mcp command"} {
		if !seen[want] {
			t.Errorf("audit log lacks %q; has %v", want, seen)
		}
	}
	if seen["小陈 live command"] {
		t.Error("a member's demo command was audited as if it reached equipment")
	}
}
