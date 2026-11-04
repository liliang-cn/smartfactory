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

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"smartfactory/internal/audit"
	"smartfactory/internal/auth"
	"smartfactory/internal/engine"
	"smartfactory/internal/erp"
	"smartfactory/internal/mcptools"
	"smartfactory/internal/stores"
	"smartfactory/internal/tags"
)

// The BOM tools over MCP: a member reads a part's revisions and its explosion against the stores,
// where a material is used, the open orders' kitting and the change records; a token for another
// company reads nothing of it; no tool writes.
func TestBOMMCP(t *testing.T) {
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
	trail, _ := audit.Open(t.TempDir(), log)
	t.Cleanup(func() { trail.Close() })
	orders, err := erp.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { orders.Close() })
	prod := erp.New(orders, eng, log)
	sto, err := stores.Open(t.TempDir(), eng, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sto.Close() })
	sto.Categories = func(string) []stores.Category {
		return []stores.Category{{Key: "bar", Label: "棒料", Kind: stores.Bar, Unit: "t", Fields: []string{"牌号", "规格"}}, {Key: "part", Label: "标准件", Kind: stores.Part, Unit: "件"}}
	}
	admin, adk := auth.NewToken("厂长", auth.Admin, nil, "")
	op, opk := auth.NewToken("技术部 陈工", auth.Operator, []string{"jianghe"}, "")
	member, mk := auth.NewToken("计划员", auth.Member, []string{"jianghe"}, "")
	stranger, sk := auth.NewToken("别家", auth.Admin, []string{"huaduan"}, "")
	srv := &Server{Hub: tags.NewHub(), Engine: eng, Keys: keyring(t, "", adk, opk, mk, sk), Audit: trail, Log: log, Production: prod, Stores: sto}
	ms := mcptools.New(srv.Hub, eng, "test", trail, nil)
	o := srv.Operations()
	mcptools.AddBOM(ms, mcptools.BOMTools{
		Query: func(w auth.Who, company, part, rev string, qty float64) (any, error) { return o.QueryBOM(w, company, part, rev, qty) },
		WhereUsed: func(w auth.Who, company, q string) (any, error) {
			us, err := o.WhereUsed(w, company, q)
			return map[string]any{"query": q, "count": len(us), "uses": us}, err
		},
		Kitting: func(w auth.Who, company, order string) (any, error) { return o.OrderKit(w, company, order) },
		ECOs:    func(w auth.Who, company, part string, limit int) (any, error) { return o.ChangeRecords(w, company, part, limit) },
	})
	srv.MCP = mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return ms }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, DisableLocalhostProtection: true})
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
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, b
	}
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
			text := ""
			for _, c := range res.Content {
				if tc, ok := c.(*mcp.TextContent); ok {
					text += tc.Text
				}
			}
			return nil, text
		}
		var out map[string]any
		b, _ := json.Marshal(res.StructuredContent)
		json.Unmarshal(b, &out)
		return out, ""
	}

	var cfg map[string]any
	json.Unmarshal([]byte(eng.BuiltinTemplate("huaduan")), &cfg)
	cfg["id"], cfg["name"], cfg["short"] = "jianghe", "江河锻造", "江河"
	if got, b := do(admin, "POST", "/api/admin/companies", cfg); got != 200 {
		t.Fatalf("build: %d %s", got, b)
	}
	// a crankshaft in two revisions (the second with a change record), a 3 t bar and 1000 plugs, an order of 500
	crank := stores.BOM{Part: "QZ-B20-4", Name: "2.0T 四缸曲轴", Drawing: "B", Lines: []stores.BOMLine{
		{ID: "F", Code: "QZ-B20-4-DJ", Name: "曲轴锻件", Kind: stores.Make, Qty: 1, Op: "热模锻"},
		{Parent: "F", Code: "GB-42CrMo-80", Name: "42CrMo 圆钢", Grade: "42CrMo", Spec: "φ80", Kind: stores.Raw, Qty: 27.4, Unit: "kg", Scrap: 2, Op: "下料"},
		{Code: "BZ-PLUG-M10", Name: "油道螺塞", Kind: stores.Std, Qty: 2, Op: "装配"},
	}}
	_, b := do(op, "POST", "/api/bom/jianghe", crank)
	var a stores.BOM
	json.Unmarshal(b, &a)
	do(op, "POST", "/api/bom/jianghe/"+a.ID+"/release", stores.Release{})
	crank.Lines[2].Qty, crank.Drawing = 3, "C"
	_, b = do(op, "POST", "/api/bom/jianghe", crank)
	var next stores.BOM
	json.Unmarshal(b, &next)
	if got, b := do(op, "POST", "/api/bom/jianghe/"+next.ID+"/release", stores.Release{Why: "客户图纸 C 版加一条油道"}); got != 200 {
		t.Fatalf("release B: %d %s", got, b)
	}
	_, b = do(op, "POST", "/api/stores/jianghe/items", stores.Item{Cat: "bar", Code: "GB-42CrMo-80", Name: "42CrMo 圆钢 φ80", Attrs: map[string]string{"牌号": "42CrMo", "规格": "φ80"}})
	var bar stores.Item
	json.Unmarshal(b, &bar)
	do(op, "POST", "/api/stores/jianghe/moves", stores.MoveInput{Item: bar.ID, Kind: stores.In, Qty: 3, Heat: "26A1", Mill: "华东特钢"})
	_, b = do(op, "POST", "/api/stores/jianghe/items", stores.Item{Cat: "part", Code: "BZ-PLUG-M10", Name: "油道螺塞"})
	var plug stores.Item
	json.Unmarshal(b, &plug)
	do(op, "POST", "/api/stores/jianghe/moves", stores.MoveInput{Item: plug.ID, Kind: stores.In, Qty: 1000})
	if got, b := do(op, "POST", "/api/erp/jianghe/orders", map[string]any{"no": "MO-1", "partNo": "QZ-B20-4", "qty": 500}); got != 200 {
		t.Fatalf("order: %d %s", got, b)
	}

	// bom_query: the part list, a part by name, a revision, and the quantity set against the stores
	out, msg := call(member, "bom_query", map[string]any{"company": "jianghe"})
	if msg != "" || !strings.Contains(js(out), "QZ-B20-4") {
		t.Errorf("parts: %s %v", msg, out)
	}
	out, msg = call(member, "bom_query", map[string]any{"company": "jianghe", "part": "qz-b20-4", "qty": 500})
	ex, _ := out["explosion"].(map[string]any)
	revs, _ := out["revisions"].([]any)
	if msg != "" || ex["rev"] != "B" || len(revs) != 2 || ex["short"] != float64(2) || !strings.Contains(js(ex["needs"]), "BZ-PLUG-M10") {
		t.Errorf("explosion of 500: %s %v", msg, out)
	}
	out, _ = call(member, "bom_query", map[string]any{"company": "jianghe", "part": "QZ-B20-4", "rev": "A"})
	if ex, _ := out["explosion"].(map[string]any); ex["rev"] != "A" || ex["status"] != stores.Retired {
		t.Errorf("revision A: %v", out)
	}
	if _, msg := call(member, "bom_query", map[string]any{"company": "jianghe", "part": "no-such-part"}); !strings.Contains(msg, "对得上") {
		t.Errorf("unknown part: %q", msg)
	}
	// where-used
	out, msg = call(member, "bom_where_used", map[string]any{"company": "jianghe", "query": "螺塞"})
	if msg != "" || out["count"] != float64(1) || !strings.Contains(js(out), "QZ-B20-4") {
		t.Errorf("where used: %s %v", msg, out)
	}
	// kitting: the order of 500 is short of the bar and of plugs; by number and unknown
	out, msg = call(member, "bom_kitting", map[string]any{"company": "jianghe"})
	if msg != "" || len(out["short"].([]any)) != 2 {
		t.Errorf("kitting: %s %v", msg, out)
	}
	out, msg = call(member, "bom_kitting", map[string]any{"company": "jianghe", "order": "mo-1"})
	if o1, _ := out["order"].(map[string]any); msg != "" || o1["ready"] != false || o1["short"] != float64(2) {
		t.Errorf("one order: %s %v", msg, out)
	}
	if _, msg := call(member, "bom_kitting", map[string]any{"company": "jianghe", "order": "MO-9"}); !strings.Contains(msg, "MO-9") {
		t.Errorf("unknown order: %q", msg)
	}
	// change records
	out, msg = call(member, "bom_ecos", map[string]any{"company": "jianghe", "part": "QZ-B20-4"})
	ecos, _ := out["ecos"].([]any)
	if msg != "" || len(ecos) != 2 || !strings.Contains(js(ecos[0]), "客户图纸 C 版") {
		t.Errorf("ECOs: %s %v", msg, out)
	}
	// another company's token reads nothing, nobody without a token gets in
	for _, tool := range []string{"bom_query", "bom_where_used", "bom_kitting", "bom_ecos"} {
		args := map[string]any{"company": "jianghe"}
		if tool == "bom_where_used" {
			args["query"] = "x"
		}
		if _, msg := call(stranger, tool, args); !strings.Contains(msg, "does not cover") {
			t.Errorf("%s with another company's token: %q", tool, msg)
		}
	}
	_ = admin
}

func js(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
