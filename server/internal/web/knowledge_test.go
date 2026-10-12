package web

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"smartfactory/internal/agents"
	"smartfactory/internal/audit"
	"smartfactory/internal/auth"
	"smartfactory/internal/engine"
	"smartfactory/internal/knowledge"
	"smartfactory/internal/mcptools"
	"smartfactory/internal/tags"
)

// The knowledge base over MCP: a member searches the company's documents and process, looks a
// thing up however its number is written, walks the graph around it and reads a document whole;
// a name that is several things answers with candidates; what comes back stays within its caps;
// a token for another company is refused as the console's API refuses it.
func TestKnowledgeMCP(t *testing.T) {
	if _, err := os.Stat("../engine/js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	t.Setenv("LLM_API_KEY", "")
	t.Setenv("AGENTGO_HOME", t.TempDir())
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng, err := engine.New(log)
	if err != nil {
		t.Fatal(err)
	}
	eng.Dir = t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); <-eng.Done })
	go eng.Run(ctx)
	other := ""
	for _, c := range eng.CompanyIDs() {
		if c != "huaduan" {
			other = c
			break
		}
	}
	ag, err := agents.New(t.TempDir(), eng, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ag.Close() })
	kb, err := knowledge.Open(t.TempDir(), eng, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(kb.Close)
	ag.KB = kb
	if err := kb.SyncProcess(ctx, "huaduan"); err != nil {
		t.Fatal(err)
	}
	if err := ag.Ensure("huaduan"); err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("压力机导轨每班加注润滑脂，滑块平行度每周用百分表检查一次。", 30)
	doc, err := ag.AddDoc(ctx, "huaduan", "热模锻压力机维护规程", "# 打击力\n\nHP-101 额定打击力 25000 kN，模具预热到 250 ℃ 以上才能打击。\n\n# 日常保养\n\nHP-101 "+long, "test")
	if err != nil {
		t.Fatal(err)
	}
	trail, _ := audit.Open(t.TempDir(), log)
	t.Cleanup(func() { trail.Close() })

	member, mk := auth.NewToken("计划员", auth.Member, []string{"huaduan"}, "")
	outsider, ok := auth.NewToken("别家", auth.Admin, []string{other}, "")
	srv := &Server{Hub: tags.NewHub(), Engine: eng, Keys: keyring(t, "", mk, ok), Audit: trail, Log: log, Agents: ag, Knowledge: kb}
	ms := mcptools.New(srv.Hub, eng, "test", trail, nil)
	mcptools.AddKnowledge(ms, srv.Operations().KnowledgeTools())
	srv.MCP = mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return ms }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, DisableLocalhostProtection: true})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

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
	status := func(token, method, path string) int {
		t.Helper()
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(`{}`))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	list := func(v any) []map[string]any {
		var out []map[string]any
		for _, x := range v.([]any) {
			out = append(out, x.(map[string]any))
		}
		return out
	}

	// who may ask: a visitor reaches neither; another company's token is refused by both
	if got := status("", "POST", "/mcp"); got != 401 {
		t.Errorf("MCP without a token: %d", got)
	}
	if got := status(outsider, "GET", "/api/knowledge/huaduan/search?q=HP-101"); got != 403 {
		t.Errorf("another company's token searched over HTTP: %d", got)
	}
	for tool, args := range map[string]map[string]any{
		"knowledge_search": {"company": "huaduan", "query": "HP-101"},
		"knowledge_entity": {"company": "huaduan", "name": "HP-101"},
		"knowledge_graph":  {"company": "huaduan"},
		"read_doc":         {"company": "huaduan", "doc": doc.ID},
	} {
		if _, msg := call(outsider, tool, args); !strings.Contains(msg, "does not cover company") {
			t.Errorf("%s with another company's token: %q", tool, msg)
		}
	}

	// search: the station written as people write it finds the document that names it
	out, msg := call(member, "knowledge_search", map[string]any{"company": "huaduan", "query": "hp101 的额定打击力是多少"})
	if msg != "" {
		t.Fatal(msg)
	}
	if !slices.Contains(out["entities"].([]any), any("HP-101")) || out["mode"] != "ppr" {
		t.Errorf("HP-101 not recognised: %v %v", out["entities"], out["mode"])
	}
	ps := list(out["passages"])
	i := slices.IndexFunc(ps, func(p map[string]any) bool {
		return p["doc"] == doc.ID && strings.Contains(p["text"].(string), "25000")
	})
	if i < 0 {
		t.Fatalf("the document was not found: %+v", ps)
	}
	if p := ps[i]; p["from"] != "doc" || p["part"] == nil || !strings.Contains(p["cite"].(string), doc.ID) {
		t.Errorf("a passage to cite: %+v", p)
	}

	// a thing: its relations both ways, the passages that name it, cut short
	out, msg = call(member, "knowledge_entity", map[string]any{"company": "huaduan", "name": "hp 101"})
	if msg != "" {
		t.Fatal(msg)
	}
	e := out["entity"].(map[string]any)
	links := list(e["links"])
	has := func(typ, dir string) bool {
		return slices.ContainsFunc(links, func(l map[string]any) bool { return l["type"] == typ && l["dir"] == dir })
	}
	if e["name"] != "HP-101" || e["kind"] != knowledge.TStation || !has("feeds", "in") || !has("feeds", "out") || !has("is_a", "out") || !has("has_station", "in") {
		t.Errorf("HP-101: %+v", e)
	}
	ms2 := list(out["passages"])
	if len(ms2) == 0 || ms2[0]["from"] != "doc" || out["passagesTotal"].(float64) < float64(len(ms2)) {
		t.Errorf("passages naming HP-101: %+v", out)
	}
	for _, p := range ms2 {
		if n := len([]rune(p["text"].(string))); n > 401 || n == 401 && p["cut"] != true {
			t.Errorf("a passage of %d runes: %+v", n, p)
		}
	}
	if !slices.ContainsFunc(ms2, func(p map[string]any) bool { return p["cut"] == true }) {
		t.Errorf("the long passage was not cut: %+v", ms2)
	}

	// a station number in several plants, a partial name: candidates; nothing: an error
	out, msg = call(member, "knowledge_entity", map[string]any{"company": "huaduan", "name": "saw-101"})
	if msg != "" || out["entity"] != nil || len(out["candidates"].([]any)) < 2 {
		t.Errorf("SAW-101 is in several plants: %+v %s", out, msg)
	}
	if _, msg = call(member, "knowledge_entity", map[string]any{"company": "huaduan", "name": "没有这台设备"}); !strings.Contains(msg, "knowledge_search") {
		t.Errorf("a name that is nothing: %q", msg)
	}

	// the graph: what it holds; around a thing; by kind, within the caps
	out, msg = call(member, "knowledge_graph", map[string]any{"company": "huaduan"})
	sum, _ := out["summary"].(map[string]any)
	if msg != "" || sum == nil || sum["kinds"].(map[string]any)["station"].(float64) < 50 || sum["library"].(float64) < 1 || sum["indexed"] != sum["library"] {
		t.Errorf("summary: %+v %s", out, msg)
	}
	out, msg = call(member, "knowledge_graph", map[string]any{"company": "huaduan", "around": "ih101"})
	nodes := list(out["nodes"])
	if msg != "" || out["center"] != "IH-101" || nodes[0]["name"] != "IH-101" || !slices.ContainsFunc(nodes, func(n map[string]any) bool { return n["name"] == "RF-101" }) {
		t.Errorf("around IH-101: %+v %s", out, msg)
	}
	out, _ = call(member, "knowledge_graph", map[string]any{"company": "huaduan", "kinds": []string{"station"}, "limit": 5})
	if nodes = list(out["nodes"]); len(nodes) != 5 || out["nodesLeftOut"].(float64) < 50 || !strings.Contains(out["note"].(string), "Cut") {
		t.Errorf("five stations: %+v", out)
	}
	out, _ = call(member, "knowledge_graph", map[string]any{"company": "huaduan", "around": "曲轴 1 线", "hops": 9, "limit": 1000})
	if n, e := len(list(out["nodes"])), len(list(out["edges"])); n > 200 || e > 400 || n == 0 {
		t.Errorf("caps: %d things, %d relations", n, e)
	}
	if _, msg = call(member, "knowledge_graph", map[string]any{"company": "huaduan", "kinds": []string{"robot"}}); !strings.Contains(msg, "no kind") {
		t.Errorf("an unknown kind: %q", msg)
	}
	out, msg = call(member, "knowledge_graph", map[string]any{"company": "huaduan", "around": "SAW-101"})
	if msg != "" || len(out["candidates"].([]any)) < 2 || out["nodes"] != nil {
		t.Errorf("around a name of several things: %+v %s", out, msg)
	}

	// a document, passage by passage
	out, msg = call(member, "read_doc", map[string]any{"company": "huaduan", "doc": doc.ID, "count": 1})
	if msg != "" || out["title"] != "热模锻压力机维护规程" || len(out["parts"].([]any)) != 1 || out["total"].(float64) < 2 || out["next"] != float64(1) {
		t.Errorf("read_doc: %+v %s", out, msg)
	}
	if _, msg = call(member, "read_doc", map[string]any{"company": "huaduan", "doc": "nope"}); msg == "" {
		t.Error("read a document that is not there")
	}
}
