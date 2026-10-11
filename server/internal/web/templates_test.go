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

// A company's new installation builds its one factory from a built-in template; its
// administrator saves the factory as a template (without the company's name), exports it as a
// file, and another installation imports it and builds from it.
func TestTemplates(t *testing.T) {
	if _, err := os.Stat("../engine/js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	std := sfptest.Load(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	type node struct{ ts *httptest.Server }
	start := func(keys *auth.Keyring) node {
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
		saved, _ := templates.Open(t.TempDir())
		ts := httptest.NewServer((&Server{Hub: tags.NewHub(), Engine: eng, Keys: keys, Audit: trail, Log: log, Saved: saved}).Handler())
		t.Cleanup(ts.Close)
		return node{ts}
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

	admin, adk := auth.NewToken("厂长", auth.Admin, nil, "")
	inst := start(keyring(t, "", adk))
	_, b := do(inst, admin, "GET", "/api/admin", nil)
	std.Check("AdminOverview", b)
	var ov struct {
		Single    bool
		Companies []any
	}
	json.Unmarshal(b, &ov)
	if !ov.Single || len(ov.Companies) != 0 {
		t.Fatalf("a new installation: %s", b)
	}
	_, b = do(inst, admin, "GET", "/api/admin/templates", nil)
	std.Check("Templates", b)
	var list []struct{ ID, Source string }
	json.Unmarshal(b, &list)
	if !slices.ContainsFunc(list, func(s struct{ ID, Source string }) bool { return s.ID == "huaduan" && s.Source == "builtin" }) {
		t.Fatalf("the built-in templates are not offered: %s", b)
	}

	// build the factory from a built-in template, under the company's own id
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
		t.Errorf("a second company on an installation: %d", got)
	}

	// save it as a template, without the company's name, and export it
	if got, b := do(inst, admin, "POST", "/api/admin/templates", map[string]any{"id": "forge-3plants", "name": "三厂锻造", "from": "jianghe"}); got != 200 {
		t.Fatalf("save: %d %s", got, b)
	} else if strings.Contains(string(b), "江河") {
		t.Errorf("the company's name went into the template: %s", b)
	}
	got, file := do(inst, admin, "GET", "/api/admin/templates/local/forge-3plants", nil)
	if got != 200 || strings.Contains(string(file), "江河") {
		t.Fatalf("export: %d %s", got, file)
	}
	std.Check("Template", file)

	// another installation imports the file and builds from it
	admin2, adk2 := auth.NewToken("厂长", auth.Admin, nil, "")
	inst2 := start(keyring(t, "", adk2))
	var exported map[string]any
	json.Unmarshal(file, &exported)
	if got, b := do(inst2, admin2, "POST", "/api/admin/templates/import", exported); got != 200 {
		t.Fatalf("import: %d %s", got, b)
	}
	if got, _ := do(inst2, admin2, "POST", "/api/admin/templates/import", map[string]any{"id": "x"}); got != 400 {
		t.Errorf("imported something that is not a template: %d", got)
	}
	_, b = do(inst2, admin2, "GET", "/api/admin/templates/local/forge-3plants", nil)
	var again struct{ Company map[string]any }
	json.Unmarshal(b, &again)
	again.Company["id"], again.Company["name"], again.Company["short"] = "hehai", "河海锻造", "河海"
	if got, b := do(inst2, admin2, "POST", "/api/admin/companies", again.Company); got != 200 {
		t.Errorf("build from the imported template: %d %s", got, b)
	}
}
