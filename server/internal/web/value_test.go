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
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"smartfactory/internal/audit"
	"smartfactory/internal/auth"
	"smartfactory/internal/engine"
	"smartfactory/internal/erp"
	"smartfactory/internal/history"
	"smartfactory/internal/mcptools"
	"smartfactory/internal/ops"
	"smartfactory/internal/sfptest"
	"smartfactory/internal/tags"
)

type plantDays []history.Day

func (p plantDays) Days(_ context.Context, from, to time.Time) ([]history.Day, error) { return p, nil }

// The value report through the API: members read their company's, nobody else's; the
// advisor's plans, the console agent's proposals, the ERP's orders and the plant's history all
// count, each figure with its n and basis; a company with no advisor and no history says so
// instead of showing numbers; the export is a CSV a spreadsheet opens; every answer holds to
// the protocol.
func TestValueReport(t *testing.T) {
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
	other := ""
	for _, c := range eng.CompanyIDs() {
		if c != "huaduan" {
			other = c
			break
		}
	}
	trail, _ := audit.Open(t.TempDir(), log)
	t.Cleanup(func() { trail.Close() })
	store, err := erp.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	prod := erp.New(store, eng, log)
	if rs := prod.Submit("huaduan", []erp.Input{{No: "MO-1", PartNo: "LG-B15", Qty: 2}, {No: "MO-2", PartNo: "LG-B15", Qty: 3}}, "push"); !rs[0].OK || !rs[1].OK {
		t.Fatalf("orders: %+v", rs)
	}

	// the advisor: one plan adopted a week ago and measured since, one rejected
	day := func(d int) string { return time.Now().AddDate(0, 0, d).Format(time.RFC3339) }
	advisor := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/consult" || r.Header.Get("Authorization") != "Bearer analyst" {
			http.Error(w, "no", 403)
			return
		}
		w.Write([]byte(`{"at":"` + day(0) + `","goals":[{"goal":{"id":"G1"},"findings":[],"plans":[
			{"plan":{"id":"P-1","does":"换模点提前","at":"` + day(-8) + `","expect":{"metric":"oee","direction":"up"}},"state":"adopted",
			 "decision":{"verdict":"adopted","at":"` + day(-7) + `","baseline":{"value":0.6,"data_days":14}},
			 "progress":{"final":false,"baseline":0.6,"target":0.63,"measured":{"value":0.64,"data_days":6},"outcome":"achieved","at":"` + day(-1) + `"}},
			{"plan":{"id":"P-2","does":"夜班停一条线","at":"` + day(-6) + `","expect":{"metric":"produced_units","direction":"up"}},"state":"rejected",
			 "decision":{"verdict":"rejected","at":"` + day(-5) + `"}}]}]}`))
	}))
	t.Cleanup(advisor.Close)

	// the plant's history: measured days around the adoption
	var days plantDays
	for d := -20; d <= 0; d++ {
		at := time.Now().AddDate(0, 0, d)
		oee := 0.6
		if d > -7 {
			oee = 0.65
		}
		days = append(days, history.Day{Day: time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC), Source: "measured",
			LineHours: 24, PlannedS: 86400, GoodIdealS: 86400 * oee, Produced: 1000, Good: 950, MachineHours: 48, DownS: 3600})
	}

	admin, ak := auth.NewToken("厂长", auth.Admin, nil, "")
	member, mk := auth.NewToken("计划员", auth.Member, []string{"huaduan"}, "")
	outsider, ok := auth.NewToken("别家", auth.Admin, []string{other}, "")
	srv := &Server{Hub: tags.NewHub(), Engine: eng, Keys: keyring(t, "", ak, mk, ok), Audit: trail, Log: log, Production: prod,
		AdvisorAPI: map[string]Advisor{"huaduan": {API: advisor.URL, Token: "analyst"}}, History: map[string]ops.PlantHistory{"huaduan": days}}
	ms := mcptools.New(srv.Hub, eng, "test", trail, nil)
	mcptools.AddValueTool(ms, srv.Operations().ValueTool)
	srv.MCP = mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return ms }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, DisableLocalhostProtection: true})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	call := func(token, tool string, args map[string]any) (map[string]any, bool) {
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
		var out map[string]any
		b, _ := json.Marshal(res.StructuredContent)
		json.Unmarshal(b, &out)
		return out, !res.IsError
	}
	do := func(token, method, path string, body any) (int, http.Header, []byte) {
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
		return res.StatusCode, res.Header, b
	}

	// the console agent proposed three changes: one confirmed, one turned down, one left
	who := auth.Who{Name: "厂长", Role: auth.Admin, Key: "x"}
	for _, id := range []string{"AP-1", "AP-2", "AP-3"} {
		trail.Record(who, "assistant", "huaduan", "agent.propose", id, "cancel_order 撤回工单 MO-1", nil)
	}
	if got, _, b := do(admin, "POST", "/api/admin/actions", map[string]any{"id": "AP-1", "kind": "cancel_order", "args": map[string]any{"company": "huaduan", "no": "MO-1"}}); got != 200 {
		t.Fatalf("confirm: %d %s", got, b)
	}
	if got, _, b := do(admin, "POST", "/api/admin/actions/dismiss", map[string]any{"id": "AP-2", "kind": "cancel_order", "args": map[string]any{"company": "huaduan", "no": "MO-2"}}); got != 200 {
		t.Fatalf("dismiss: %d %s", got, b)
	}
	if got, _, _ := do(member, "POST", "/api/admin/actions/dismiss", map[string]any{"id": "AP-3", "kind": "cancel_order", "args": map[string]any{"company": "huaduan"}}); got != 403 {
		t.Errorf("a member turned a proposal down: %d", got)
	}

	for _, c := range []struct {
		token, path string
		want        int
	}{
		{"", "/api/value/huaduan", 401},
		{outsider, "/api/value/huaduan", 403},
		{member, "/api/value/nope", 404},
		{member, "/api/value/huaduan?days=0", 400},
		{member, "/api/value/huaduan?from=2026-09-30&to=2026-09-01", 400},
	} {
		if got, _, b := do(c.token, "GET", c.path, nil); got != c.want {
			t.Errorf("%s: %d %s, want %d", c.path, got, b, c.want)
		}
	}

	got, _, b := do(member, "GET", "/api/value/huaduan?days=30", nil)
	if got != 200 {
		t.Fatalf("report: %d %s", got, b)
	}
	std.Check("ValueReport", b)
	var v ops.Value
	json.Unmarshal(b, &v)
	fig := func(s ops.Section, key string) ops.Figure {
		t.Helper()
		i := slices.IndexFunc(s.Figures, func(f ops.Figure) bool { return f.Key == key })
		if i < 0 {
			t.Fatalf("no figure %s", key)
		}
		return s.Figures[i]
	}
	is := func(f ops.Figure, value float64, n int) {
		t.Helper()
		if f.Value == nil || *f.Value != value || f.N != n || f.Basis == "" {
			t.Errorf("%s = %s (n %d), want %v (n %d)", f.Key, f.Text(), f.N, value, n)
		}
	}
	is(fig(v.Suggestions.Section, "plans.adoption"), 0.5, 2)
	is(fig(v.Suggestions.Section, "plans.better"), 1, 1)
	if len(v.Suggestions.Plans) != 1 || v.Suggestions.Plans[0].ID != "P-1" || v.Suggestions.Plans[0].Label != "OEE" {
		t.Errorf("plans: %+v", v.Suggestions.Plans)
	}
	is(fig(v.AI, "agent.proposed"), 3, 3)
	is(fig(v.AI, "agent.confirmed"), 1, 1)
	is(fig(v.AI, "agent.dismissed"), 1, 1)
	is(fig(v.AI, "agent.open"), 1, 1)
	is(fig(v.Orders, "orders.received"), 2, 2)
	if f := fig(v.Plant.Section, "plant.oee"); f.Value == nil || f.Source != "measured" || f.N == 0 {
		t.Errorf("plant OEE: %+v", f)
	}
	if v.Plant.Source != "measured" || len(v.Plant.Weeks) < 4 || v.Plant.Compare == nil || v.Plant.Compare.Plan != "P-1" ||
		*v.Plant.Compare.Before.OEE != 0.6 || *v.Plant.Compare.After.OEE != 0.65 {
		t.Errorf("plant: %+v %+v", v.Plant.Weeks, v.Plant.Compare)
	}
	// no guard and no calibration on this server: those parts say so, with no numbers
	if v.Calibration.Missing == "" || fig(v.AI, "commands.held").Value != nil {
		t.Errorf("parts without a source: %q %+v", v.Calibration.Missing, fig(v.AI, "commands.held"))
	}

	// the same report for an assistant over MCP
	if out, ok := call(member, "value_report", map[string]any{"company": "huaduan", "days": 30}); !ok || out["suggestions"] == nil || out["plant"] == nil {
		t.Errorf("value_report: %v", out)
	}
	if _, ok := call(outsider, "value_report", map[string]any{"company": "huaduan"}); ok {
		t.Error("value_report answered a token for another company")
	}
	if _, ok := call(member, "value_report", map[string]any{"company": "huaduan", "from": "2026-09-01"}); ok {
		t.Error("value_report took a from without a to")
	}

	// a company with no advisor and no history: words, not numbers
	got, _, b = do(admin, "GET", "/api/value/"+other+"?days=7", nil)
	if got != 200 {
		t.Fatalf("report of %s: %d %s", other, got, b)
	}
	std.Check("ValueReport", b)
	var empty ops.Value
	json.Unmarshal(b, &empty)
	if empty.Suggestions.Missing != ops.ErrNoAdvisor.Error() || empty.Plant.Missing == "" || empty.Plant.Compare != nil || empty.Period.Days != 7 {
		t.Errorf("no advisor, no history: %+v / %+v", empty.Suggestions.Section, empty.Plant.Section)
	}
	for _, f := range append(empty.Suggestions.Figures, empty.Plant.Figures...) {
		if f.Value != nil {
			t.Errorf("%s = %s with nothing to count from", f.Key, f.Text())
		}
	}

	// the export
	got, h, b := do(member, "GET", "/api/value/huaduan?from="+time.Now().AddDate(0, 0, -6).Format("2006-01-02")+"&to="+time.Now().Format("2006-01-02")+"&format=csv", nil)
	if got != 200 || !strings.HasPrefix(h.Get("Content-Type"), "text/csv") || !strings.Contains(h.Get("Content-Disposition"), "value-huaduan-") {
		t.Fatalf("export: %d %v", got, h)
	}
	if s := string(b); !strings.Contains(s, "成效报告") || !strings.Contains(s, "采纳率") || !strings.Contains(s, "P-1") || !strings.Contains(s, "不证明") {
		t.Errorf("export:\n%s", s)
	}
	if got, _, _ := do(outsider, "GET", "/api/value/huaduan?format=csv", nil); got != 403 {
		t.Errorf("an outsider exported: %d", got)
	}

	entries, _ := trail.List(t.Context(), "huaduan", 0, 50)
	var actions []string
	for _, e := range entries {
		actions = append(actions, e.Action+" "+e.Target)
	}
	for _, want := range []string{"agent.confirm AP-1", "agent.dismiss AP-2", "erp.cancel MO-1"} {
		if !slices.Contains(actions, want) {
			t.Errorf("not in the activity log: %s (have %v)", want, actions)
		}
	}
}
