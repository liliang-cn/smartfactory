package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"smartfactory/internal/audit"
	"smartfactory/internal/auth"
	"smartfactory/internal/engine"
	"smartfactory/internal/knowledge"
	"smartfactory/internal/mcptools"
	"smartfactory/internal/sfptest"
	"smartfactory/internal/tags"
)

// A company's ontology over HTTP and MCP: a member reads its classes and constraints, the things
// of a class filtered by a relation, a thing with why each inferred fact holds; an administrator
// asks it in SPARQL, a member may not; another company's token is refused. A freezer reading
// above its limit for long enough raises an alarm on the machine, goes into the activity log and
// shows as the constraint violated. The answers hold to the protocol.
func TestOntologyHTTPAndMCP(t *testing.T) {
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
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); <-eng.Done })
	go eng.Run(ctx)
	for _, f := range []string{"chuan-pack.json", "chuan-company.json"} {
		raw, err := os.ReadFile("../knowledge/testdata/" + f)
		if err != nil {
			t.Fatal(err)
		}
		r := eng.CreateIndustry(raw)
		if f == "chuan-company.json" {
			r = eng.CreateCompany(raw)
		}
		if !r.OK {
			t.Fatal(f, r.Errors)
		}
	}
	trail, _ := audit.Open(t.TempDir(), log)
	t.Cleanup(func() { trail.Close() })
	dir := t.TempDir()
	kb, err := knowledge.Open(dir, eng, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(kb.Close)
	live := knowledge.NewLive(dir, log)
	live.Alert, live.Audit, live.Changed = eng.Alert, trail, kb.EventsChanged
	kb.Live = live
	go live.Run(ctx)
	hub := tags.NewHub()
	hub.Tap = live.Add
	if err := kb.SyncProcess(ctx, "chuan"); err != nil {
		t.Fatal(err)
	}

	member, mk := auth.NewToken("服务员", auth.Member, []string{"chuan"}, "")
	admin, ak := auth.NewToken("店长", auth.Admin, []string{"chuan"}, "")
	outsider, ok := auth.NewToken("别家", auth.Admin, []string{"huaduan"}, "")
	srv := &Server{Hub: hub, Engine: eng, Keys: keyring(t, "", mk, ak, ok), Audit: trail, Log: log, Knowledge: kb}
	ms := mcptools.New(hub, eng, "test", trail, nil)
	mcptools.AddKnowledge(ms, srv.Operations().KnowledgeTools())
	srv.MCP = mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return ms }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, DisableLocalhostProtection: true})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	do := func(token, method, path string, body any) (int, []byte) {
		t.Helper()
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, ts.URL+path, rd)
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, b
	}
	get := func(path, def string) []byte {
		t.Helper()
		code, b := do(member, "GET", path, nil)
		if code != 200 {
			t.Fatalf("%s: %d %s", path, code, b)
		}
		std.Check(def, b)
		return b
	}

	var o knowledge.OntOverview
	json.Unmarshal(get("/api/knowledge/chuan/ontology", "Ontology"), &o)
	if !o.Declared || len(o.Rules) != 1 || len(o.Constraints) != 2 {
		t.Errorf("the ontology: %+v", o)
	}
	var in knowledge.OntInstances
	json.Unmarshal(get("/api/knowledge/chuan/ontology/instances?class="+url.QueryEscape("菜品")+"&relation="+url.QueryEscape("含过敏原")+"&value="+url.QueryEscape("花生过敏原"), "OntologyInstances"), &in)
	if in.Total != 2 || in.Instances[0].Name != "牛油锅底" {
		t.Errorf("dishes with peanut: %+v", in)
	}
	var th knowledge.OntThing
	json.Unmarshal(get("/api/knowledge/chuan/ontology/thing?name="+url.QueryEscape("牛油锅底"), "OntologyThing"), &th)
	if !slices.ContainsFunc(th.Facts, func(f knowledge.OntFact) bool { return f.Relation == "含过敏原" && f.Inferred && len(f.Why) > 0 }) {
		t.Errorf("牛油锅底: %+v", th.Facts)
	}
	get("/api/knowledge/chuan/ontology/thing?name="+url.QueryEscape("毛肚"), "OntologyThing")
	get("/api/knowledge/chuan/ontology/thing?name=fz101", "OntologyThing")
	var g knowledge.OntGraph
	json.Unmarshal(get("/api/knowledge/chuan/ontology/graph", "OntologyGraph"), &g)
	if !slices.Contains(g.Types, knowledge.OntGraphType{Thing: "FZ-101", Class: "冷藏设备"}) || !slices.ContainsFunc(g.Classes, func(c knowledge.OntGraphClass) bool { return c.Name == "设备" && c.Builtin }) {
		t.Errorf("the classes to draw: %+v", g)
	}
	if code, _ := do(member, "GET", "/api/knowledge/chuan/ontology/instances?class="+url.QueryEscape("海鲜"), nil); code != 404 {
		t.Errorf("a class nobody declared: %d", code)
	}
	if code, _ := do(outsider, "GET", "/api/knowledge/chuan/ontology", nil); code != 403 {
		t.Errorf("another company's token: %d", code)
	}
	q := map[string]string{"query": "SELECT ?x WHERE { ?x a c:锅底 }"}
	if code, _ := do(member, "POST", "/api/knowledge/chuan/ontology/sparql", q); code != 403 {
		t.Errorf("a member asked SPARQL: %d", code)
	}
	code, b := do(admin, "POST", "/api/knowledge/chuan/ontology/sparql", q)
	if code != 200 {
		t.Fatalf("SPARQL: %d %s", code, b)
	}
	std.Check("SPARQLAnswer", b)
	var a knowledge.SPARQLAnswer
	json.Unmarshal(b, &a)
	if len(a.Rows) != 2 {
		t.Errorf("SPARQL: %+v", a)
	}
	if code, _ := do(admin, "POST", "/api/knowledge/chuan/ontology/sparql", map[string]string{"query": "DELETE WHERE { ?s ?p ?o }"}); code != 400 {
		t.Errorf("SPARQL that writes: %d", code)
	}

	// MCP
	call := func(token, tool string, args map[string]any) (map[string]any, string) {
		t.Helper()
		cl := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
		s, err := cl.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", HTTPClient: &http.Client{Transport: bearer{token}}, DisableStandaloneSSE: true}, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		res, err := s.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		if res.IsError {
			var msg string
			for _, c := range res.Content {
				if tc, ok := c.(*mcp.TextContent); ok {
					msg += tc.Text
				}
			}
			return nil, msg
		}
		var out map[string]any
		b, _ := json.Marshal(res.StructuredContent)
		json.Unmarshal(b, &out)
		return out, ""
	}
	out, msg := call(member, "ontology", map[string]any{"company": "chuan"})
	if msg != "" || out["overview"].(map[string]any)["declared"] != true {
		t.Errorf("ontology: %v %s", out, msg)
	}
	out, msg = call(member, "ontology", map[string]any{"company": "chuan", "class": "冷藏设备"})
	if msg != "" || out["class"].(map[string]any)["instances"].(map[string]any)["total"].(float64) != 1 {
		t.Errorf("ontology of a class: %v %s", out, msg)
	}
	out, msg = call(member, "ontology_instances", map[string]any{"company": "chuan", "class": "菜品", "relation": "含过敏原", "value": "花生过敏原"})
	if msg != "" || out["total"].(float64) != 2 {
		t.Errorf("ontology_instances: %v %s", out, msg)
	}
	out, msg = call(member, "ontology_explain", map[string]any{"company": "chuan", "name": "牛油锅底", "relation": "含过敏原"})
	facts, _ := out["facts"].([]any)
	if why, _ := facts[0].(map[string]any)["why"].([]any); msg != "" || len(facts) != 1 || !strings.Contains(why[len(why)-1].(string), "→ 牛油锅底 含过敏原 花生过敏原") {
		t.Errorf("ontology_explain: %v %s", out, msg)
	}
	for tool, args := range map[string]map[string]any{"ontology": {"company": "chuan"}, "ontology_instances": {"company": "chuan", "class": "菜品"}, "ontology_explain": {"company": "chuan", "name": "牛油锅底"}} {
		if _, msg := call(outsider, tool, args); !strings.Contains(msg, "does not cover company") {
			t.Errorf("%s with another company's token: %q", tool, msg)
		}
	}

	// the freezer above 4 ℃ for more than its 3 s: an alarm on the machine, the activity log
	t0 := time.Now().Add(-10 * time.Second)
	for i := range 6 {
		hub.Publish(tags.Tag{Company: "chuan", Plant: "s1", Asset: "FZ-101", Field: "temp", Value: 7.5 + float64(i)/10, Source: "plc", At: t0.Add(time.Duration(i) * time.Second)})
	}
	deadline := time.Now().Add(5 * time.Second)
	var st engine.AssetState
	for time.Now().Before(deadline) {
		if st, _ = eng.AssetState("chuan", "s1", "FZ-101"); slices.ContainsFunc(st.Alarms, func(a string) bool { return strings.Contains(a, "冷藏要求不高于 4") }) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !slices.ContainsFunc(st.Alarms, func(a string) bool { return strings.HasPrefix(a, "FZ-101 温度 ") && strings.HasSuffix(a, "冷藏要求不高于 4 ℃") }) {
		t.Fatalf("the alarm on the freezer: %v", st.Alarms)
	}
	json.Unmarshal(get("/api/knowledge/chuan/ontology", "Ontology"), &o)
	for _, c := range o.Constraints {
		if c.ID == "冷链" && (c.Status != "violated" || len(c.Violations) != 1 || c.Violations[0].Name != "FZ-101") {
			t.Errorf("the live constraint: %+v", c)
		}
	}
	code, b = do(admin, "GET", "/api/audit/chuan", nil)
	if code != 200 || !strings.Contains(string(b), `"ontology.violation"`) {
		t.Errorf("the activity log: %d %s", code, b)
	}
	std.Check("AuditLog", b)
}
