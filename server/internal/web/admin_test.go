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

	"smartfactory/internal/audit"
	"smartfactory/internal/auth"
	"smartfactory/internal/engine"
	"smartfactory/internal/knowledge"
	"smartfactory/internal/sfptest"
	"smartfactory/internal/tags"
)

// The console: an administrator of every company creates a company and gives people tokens that
// work at once; revoking one stops it at once; an administrator of one company manages only that
// company's tokens and cannot create companies; everything lands in the audit log.
func TestAdminConsole(t *testing.T) {
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
	defer cancel()
	go eng.Run(ctx)
	trail, err := audit.Open(t.TempDir(), log)
	if err != nil {
		t.Fatal(err)
	}
	defer trail.Close()
	store, err := auth.OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	root, rk := auth.NewToken("李工", auth.Admin, nil, "")
	local, lk := auth.NewToken("华锻管理员", auth.Admin, []string{"huaduan"}, "")
	member, mk := auth.NewToken("小陈", auth.Member, []string{"huaduan"}, "")
	keys := keyring(t, "", rk, lk, mk)
	if err := keys.Attach(store); err != nil {
		t.Fatal(err)
	}
	kb, err := knowledge.Open(t.TempDir(), eng, log)
	if err != nil {
		t.Fatal(err)
	}
	defer kb.Close()
	srv := &Server{Hub: tags.NewHub(), Engine: eng, Keys: keys, Audit: trail, Log: log, Knowledge: kb}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	do := func(token, method, path string, body any) (int, map[string]any, []byte) {
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
		var m map[string]any
		json.Unmarshal(b, &m)
		return res.StatusCode, m, b
	}

	for token, want := range map[string]int{"": 401, member: 403, local: 200, root: 200} {
		if got, _, b := do(token, "GET", "/api/admin", nil); got != want {
			t.Errorf("console overview: %d, want %d: %s", got, want, b)
		}
	}
	_, _, b := do(root, "GET", "/api/admin", nil)
	std.Check("AdminOverview", b)
	_, over, _ := do(local, "GET", "/api/admin", nil)
	if cs := over["companies"].([]any); len(cs) != 1 || cs[0].(map[string]any)["id"] != "huaduan" {
		t.Errorf("an administrator of huaduan sees: %v", cs)
	}

	// a new company, from the built-in one's lines
	company := map[string]any{
		"id": "xinghe", "name": "星河锻造", "short": "星河", "industry": "forging",
		"plants": []any{map[string]any{"id": "p1", "name": "一厂", "lines": []any{
			map[string]any{"name": "曲轴 {n} 线", "product": "crankshaft", "route": "hot-die-crank", "repeat": 2},
		}}},
	}
	// the architect's dry run: checked and laid out, nothing created
	raw, _ := json.Marshal(company)
	if r := eng.CheckCompany(raw); !r.OK || slices.Contains(eng.CompanyIDs(), "xinghe") {
		t.Errorf("check: %+v, companies %v", r, eng.CompanyIDs())
	}
	if js := eng.CompanyConfig("huaduan"); !strings.Contains(js, `"plants"`) {
		t.Errorf("a built-in company's config: %.80s", js)
	}
	if got, _, b := do(local, "POST", "/api/admin/companies", company); got != 403 {
		t.Errorf("an administrator of one company created another: %d %s", got, b)
	}
	bad := map[string]any{"id": "../x", "name": "x", "short": "x", "industry": "forging", "plants": company["plants"]}
	if got, m, b := do(root, "POST", "/api/admin/companies", bad); got != 422 || len(m["errors"].([]any)) == 0 {
		t.Errorf("a path for an id: %d %v", got, m)
	} else {
		std.Check("ConfigProblem", b)
	}
	if got, _, b := do(root, "POST", "/api/admin/companies", company); got != 200 {
		t.Fatalf("create company: %d %s", got, b)
	} else {
		std.Check("CompanyCreated", b)
	}
	if _, err := os.Stat(eng.Dir + "/configs/companies/xinghe.json"); err != nil {
		t.Error("the created company is not kept")
	}
	if got, _, b := do(root, "DELETE", "/api/admin/companies/huaduan", nil); got != 422 {
		t.Errorf("deleted a built-in company: %d %s", got, b)
	}

	// tokens: issued, working at once, revoked, not working at once
	if got, _, _ := do(local, "POST", "/api/admin/tokens", map[string]any{"name": "张工", "role": "operator", "companies": nil}); got != 400 {
		t.Errorf("an administrator of one company issued a token for every company: %d", got)
	}
	if got, _, _ := do(local, "POST", "/api/admin/tokens", map[string]any{"name": "张工", "role": "operator", "companies": []string{"xinghe"}}); got != 400 {
		t.Errorf("an administrator of huaduan issued a token for xinghe: %d", got)
	}
	got, m, b := do(local, "POST", "/api/admin/tokens", map[string]any{"name": "张工", "role": "operator", "companies": []string{"huaduan"}, "expires": "2099-12-31"})
	if got != 200 {
		t.Fatalf("issue: %d %s", got, b)
	}
	std.Check("IssuedToken", b)
	zhang := m["token"].(string)
	key := m["account"].(map[string]any)["key"].(string)
	if w, ok := keys.Lookup(zhang); !ok || w.Name != "张工" || !w.Can("huaduan", auth.Operator) || w.Can("xinghe", auth.Member) {
		t.Errorf("the issued token: %+v %v", w, ok)
	}
	if got, _, _ := do(zhang, "GET", "/api/audit/huaduan", nil); got != 200 {
		t.Errorf("the issued token does not work: %d", got)
	}
	var list []auth.Account
	_, _, b = do(root, "GET", "/api/admin/tokens", nil)
	std.Check("Accounts", b)
	json.Unmarshal(b, &list)
	if len(list) != 4 {
		t.Errorf("an administrator of every company sees %d tokens, want 4", len(list))
	}
	_, _, b = do(local, "GET", "/api/admin/tokens", nil)
	json.Unmarshal(b, &list)
	for _, a := range list {
		if len(a.Companies) == 0 {
			t.Errorf("an administrator of huaduan sees %q, a token for every company", a.Name)
		}
	}
	if got, _, _ := do(local, "DELETE", "/api/admin/tokens/"+rk.SHA256[:8], nil); got != 404 {
		t.Errorf("an administrator of huaduan reached a token for every company: %d", got)
	}
	if got, _, _ := do(root, "DELETE", "/api/admin/tokens/"+mk.SHA256[:8], nil); got != 409 {
		t.Errorf("revoked a token from the config file: %d", got)
	}
	if got, _, b := do(local, "DELETE", "/api/admin/tokens/"+key, nil); got != 200 {
		t.Fatalf("revoke: %d %s", got, b)
	}
	if got, _, _ := do(zhang, "GET", "/api/audit/huaduan", nil); got != 403 {
		t.Errorf("a revoked token still works: %d", got)
	}

	// a server restarted: issued tokens come back, revoked ones do not
	again := keyring(t, "")
	if err := again.Attach(store); err != nil {
		t.Fatal(err)
	}
	if _, ok := again.Lookup(zhang); ok {
		t.Error("a revoked token came back after a restart")
	}

	// the knowledge base: members of the company read it, its administrators rebuild it
	if err := kb.SyncProcess(t.Context(), "huaduan"); err != nil {
		t.Fatal(err)
	}
	for token, want := range map[string]int{"": 401, member: 200, local: 200} {
		if got, _, b := do(token, "GET", "/api/knowledge/huaduan", nil); got != want {
			t.Errorf("knowledge stats: %d, want %d: %s", got, want, b)
		} else if got == 200 {
			std.Check("KnowledgeOverview", b)
		}
	}
	_, _, b = do(member, "GET", "/api/knowledge/huaduan/search?q=IH-101", nil)
	std.Check("KnowledgeAnswer", b)
	_, m, b = do(member, "GET", "/api/knowledge/huaduan/entity?name=IH-101", nil)
	std.Check("EntityAnswer", b)
	if m["entity"] == nil {
		t.Errorf("IH-101 is not in the graph: %s", b)
	}
	_, _, b = do(member, "GET", "/api/knowledge/huaduan/graph", nil)
	std.Check("KnowledgeGraph", b)
	if got, _, _ := do(member, "POST", "/api/knowledge/huaduan/rebuild", nil); got != 403 {
		t.Errorf("a member rebuilt the knowledge base: %d", got)
	}
	if got, _, b := do(local, "POST", "/api/knowledge/huaduan/rebuild", nil); got != 200 {
		t.Errorf("rebuild: %d %s", got, b)
	}

	// actions: what the console agent proposes runs as whoever confirms it
	act := map[string]any{"kind": "issue_token", "args": map[string]any{"name": "王工", "role": "member", "companies": []string{"huaduan"}}}
	if got, _, b := do(local, "POST", "/api/admin/actions", act); got != 200 {
		t.Errorf("action: %d %s", got, b)
	} else {
		std.Check("IssuedToken", b)
	}
	if got, _, _ := do(local, "POST", "/api/admin/actions", map[string]any{"kind": "delete_company", "args": map[string]any{"id": "xinghe"}}); got != 403 {
		t.Errorf("an administrator of huaduan deleted xinghe through an action: %d", got)
	}
	if got, _, _ := do(root, "POST", "/api/admin/actions", map[string]any{"kind": "drop_everything"}); got != 400 {
		t.Errorf("an unknown action: %d", got)
	}

	// the log
	if got, _, _ := do(local, "GET", "/api/admin/audit", nil); got != 403 {
		t.Errorf("an administrator of one company read every company's log: %d", got)
	}
	var entries []audit.Entry
	_, _, b = do(root, "GET", "/api/admin/audit", nil)
	std.Check("AuditLog", b)
	json.Unmarshal(b, &entries)
	seen := map[string]bool{}
	for _, e := range entries {
		seen[e.Who+" "+e.Action+" "+e.Company] = true
	}
	for _, want := range []string{"李工 company.create xinghe", "李工 company.create ../x", "华锻管理员 token.issue huaduan", "华锻管理员 token.revoke huaduan"} {
		if !seen[want] {
			t.Errorf("log lacks %q: %v", want, seen)
		}
	}
}
