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
	"testing"
	"testing/fstest"

	"smartfactory/internal/config"
	"smartfactory/internal/engine"
	"smartfactory/internal/tags"
)

// A customer's own instance runs its company and nothing else; the platform server, which runs
// none, sends a factory page of a customer with an instance there and is otherwise the console.
func TestInstances(t *testing.T) {
	if _, err := os.Stat("../engine/js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	serve := func(only []string, instances []config.Instance) (*engine.Engine, *httptest.Server) {
		eng, err := engine.New(log)
		if err != nil {
			t.Fatal(err)
		}
		eng.Dir = t.TempDir()
		if err := eng.Only(only); err != nil {
			t.Fatal(err)
		}
		eng.LoadConfigs()
		ctx, cancel := context.WithCancel(t.Context())
		t.Cleanup(cancel)
		go eng.Run(ctx)
		srv := &Server{Hub: tags.NewHub(), Engine: eng, Keys: keyring(t, ""), Log: log, Instances: instances,
			Static: fstest.MapFS{"index.html": {Data: []byte("<!doctype html>")}, "admin.html": {Data: []byte("<!doctype html>")}}}
		ts := httptest.NewServer(srv.Handler())
		t.Cleanup(ts.Close)
		return eng, ts
	}
	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	eng, ts := serve([]string{"huaduan"}, nil)
	if ids := eng.CompanyIDs(); !slices.Equal(ids, []string{"huaduan"}) {
		t.Fatalf("an instance of huaduan runs %v", ids)
	}
	cfg, _ := json.Marshal(map[string]any{"id": "xinghe", "name": "x", "short": "x", "industry": "forging",
		"plants": []any{map[string]any{"id": "p1", "name": "一厂", "lines": []any{map[string]any{"name": "曲轴 {n} 线", "product": "crankshaft", "route": "hot-die-crank"}}}}})
	if r := eng.CreateCompany(cfg); r.OK {
		t.Error("an instance of huaduan created another company")
	}
	res, err := http.Get(ts.URL + "/api/site")
	if err != nil {
		t.Fatal(err)
	}
	var site struct{ Companies []string }
	json.NewDecoder(res.Body).Decode(&site)
	res.Body.Close()
	if !slices.Equal(site.Companies, []string{"huaduan"}) {
		t.Errorf("site: %v", site.Companies)
	}

	// a platform server whose data still holds a company created at run time runs none of it
	pf, platform := serve([]string{}, []config.Instance{{ID: "huaduan", Name: "华锻集团", URL: "https://huaduan.factory.superleo.cn"}})
	if r := pf.CreateCompany(cfg); r.OK {
		t.Error("the platform server created a company")
	}
	if ids := pf.CompanyIDs(); len(ids) != 0 {
		t.Errorf("the platform server runs %v", ids)
	}
	res, err = noFollow.Get(platform.URL + "/?company=huaduan&plant=p2")
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 302 || res.Header.Get("Location") != "https://huaduan.factory.superleo.cn/?plant=p2" {
		t.Errorf("an old link: %d → %s", res.StatusCode, res.Header.Get("Location"))
	}
	res, _ = noFollow.Get(platform.URL + "/")
	res.Body.Close()
	if res.StatusCode != 302 || res.Header.Get("Location") != "/admin" {
		t.Errorf("the platform's own page: %d → %s", res.StatusCode, res.Header.Get("Location"))
	}
}
