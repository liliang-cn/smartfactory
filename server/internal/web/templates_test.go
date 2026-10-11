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
	"smartfactory/internal/sfptest"
	"smartfactory/internal/tags"
	"smartfactory/internal/templates"
)

// A self-hosted instance starts empty and builds its one factory from a template; its
// administrator saves the factory as a template and publishes it to the hub, where anyone reads
// it and only its author replaces it; another instance starts from it.
func TestTemplates(t *testing.T) {
	if _, err := os.Stat("../engine/js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	std := sfptest.Load(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	type node struct {
		eng *engine.Engine
		ts  *httptest.Server
	}
	start := func(single, hub bool, market *templates.Hub, keys *auth.Keyring) node {
		eng, err := engine.New(log)
		if err != nil {
			t.Fatal(err)
		}
		eng.Dir = t.TempDir()
		if single {
			eng.Single()
		} else {
			eng.Only([]string{})
		}
		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)
		go eng.Run(ctx)
		trail, _ := audit.Open(t.TempDir(), log)
		t.Cleanup(func() { trail.Close() })
		saved, _ := templates.Open(t.TempDir())
		srv := &Server{Hub: tags.NewHub(), Engine: eng, Keys: keys, Audit: trail, Log: log, Saved: saved, Market: market}
		if hub {
			srv.HubStore, _ = templates.Open(t.TempDir())
		}
		ts := httptest.NewServer(srv.Handler())
		t.Cleanup(ts.Close)
		return node{eng, ts}
	}
	do := func(n node, token, method, path string, body any) (int, []byte) {
		t.Helper()
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = strings.NewReader(string(b))
		}
		req, _ := http.NewRequest(method, n.ts.URL+path, rd)
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

	// the hub: anyone with an account there publishes
	alice, ak := auth.NewToken("小李", auth.Member, nil, "")
	bob, bk := auth.NewToken("老王", auth.Member, nil, "")
	hub := start(false, true, nil, keyring(t, "", ak, bk))

	// an instance with an account on the hub
	admin, adk := auth.NewToken("厂长", auth.Admin, nil, "")
	inst := start(true, false, &templates.Hub{URL: hub.ts.URL, Token: alice}, keyring(t, "", adk))

	_, b := do(inst, admin, "GET", "/api/admin", nil)
	std.Check("AdminOverview", b)
	var ov struct {
		Single     bool
		Companies  []any
		HubPublish bool
	}
	json.Unmarshal(b, &ov)
	if !ov.Single || len(ov.Companies) != 0 || !ov.HubPublish {
		t.Fatalf("a new instance: %s", b)
	}
	_, b = do(inst, admin, "GET", "/api/admin/templates", nil)
	std.Check("Templates", b)
	var list []struct{ ID, Source string }
	json.Unmarshal(b, &list)
	if !slices.ContainsFunc(list, func(s struct{ ID, Source string }) bool { return s.ID == "huaduan" && s.Source == "builtin" }) {
		t.Fatalf("the built-in templates are not offered: %s", b)
	}

	// build the factory from a built-in template, under the customer's own id
	_, b = do(inst, admin, "GET", "/api/admin/templates/builtin/huaduan", nil)
	std.Check("Template", b)
	var tpl struct{ Company map[string]any }
	json.Unmarshal(b, &tpl)
	tpl.Company["id"], tpl.Company["name"], tpl.Company["short"] = "jianghe", "江河锻造", "江河"
	if got, b := do(inst, admin, "POST", "/api/admin/companies", tpl.Company); got != 200 {
		t.Fatalf("build from the template: %d %s", got, b)
	}
	other := map[string]any{"id": "another", "name": "另一家", "short": "另", "industry": "forging", "plants": tpl.Company["plants"]}
	if got, _ := do(inst, admin, "POST", "/api/admin/companies", other); got != 422 {
		t.Errorf("a second company on a self-hosted instance: %d", got)
	}

	// save it as a template, without the customer's name, and publish it
	if got, b := do(inst, admin, "POST", "/api/admin/templates", map[string]any{"id": "forge-3plants", "name": "三厂锻造", "from": "jianghe"}); got != 200 {
		t.Fatalf("save: %d %s", got, b)
	} else if strings.Contains(string(b), "江河") {
		t.Errorf("the customer's name went into the template: %s", b)
	}
	if got, b := do(inst, admin, "POST", "/api/admin/templates/forge-3plants/publish", nil); got != 200 {
		t.Fatalf("publish: %d %s", got, b)
	}
	got, b := do(hub, "", "GET", "/api/hub/templates", nil)
	std.Check("Templates", b)
	if got != 200 || !strings.Contains(string(b), `"forge-3plants"`) || !strings.Contains(string(b), "小李") {
		t.Errorf("the hub lists: %d %s", got, b)
	}
	var mine templates.Template
	_, b = do(hub, "", "GET", "/api/hub/templates/forge-3plants", nil)
	json.Unmarshal(b, &mine)
	if got, _ := do(hub, bob, "POST", "/api/hub/templates", mine); got != 409 {
		t.Errorf("someone else replaced a published template: %d", got)
	}
	if got, _ := do(hub, "", "POST", "/api/hub/templates", mine); got != 401 {
		t.Errorf("published without an account: %d", got)
	}

	// a second instance starts from the published template
	admin2, adk2 := auth.NewToken("厂长", auth.Admin, nil, "")
	inst2 := start(true, false, &templates.Hub{URL: hub.ts.URL}, keyring(t, "", adk2))
	_, b = do(inst2, admin2, "GET", "/api/admin/templates", nil)
	if !strings.Contains(string(b), `"forge-3plants"`) {
		t.Errorf("the hub's template is not offered to another instance: %s", b)
	}
	if got, b := do(inst2, admin2, "GET", "/api/admin/templates/hub/forge-3plants", nil); got != 200 {
		t.Errorf("read the hub's template: %d %s", got, b)
	}
	if got, _ := do(inst2, admin2, "POST", "/api/admin/templates/forge-3plants/publish", nil); got != 404 {
		t.Errorf("published a template it does not have: %d", got)
	}
}
