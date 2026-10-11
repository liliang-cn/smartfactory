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
	"testing/fstest"

	"smartfactory/internal/engine"
	"smartfactory/internal/tags"
)

// A company's own installation runs its company and nothing else; a new one is empty and opens
// on its console until its factory is built, and then runs that one.
func TestInstances(t *testing.T) {
	if _, err := os.Stat("../engine/js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	// only nil: a self-hosted installation (one company, empty until built)
	serve := func(only []string, _ any) (*engine.Engine, *httptest.Server) {
		eng, err := engine.New(log)
		if err != nil {
			t.Fatal(err)
		}
		eng.Dir = t.TempDir()
		if only == nil {
			err = eng.Single()
		} else {
			err = eng.Only(only)
		}
		if err != nil {
			t.Fatal(err)
		}
		eng.LoadConfigs()
		ctx, cancel := context.WithCancel(t.Context())
		// the engine writes its state when it stops: let it finish before the directory goes
		t.Cleanup(func() { cancel(); <-eng.Done })
		go eng.Run(ctx)
		srv := &Server{Hub: tags.NewHub(), Engine: eng, Keys: keyring(t, ""), Log: log,
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

	// an installation whose factory is not built yet opens on its console
	empty, ets := serve(nil, nil)
	if ids := empty.CompanyIDs(); len(ids) != 0 {
		t.Fatalf("a new installation runs %v", ids)
	}
	res, _ = noFollow.Get(ets.URL + "/")
	res.Body.Close()
	if res.StatusCode != 302 || res.Header.Get("Location") != "/admin" {
		t.Errorf("a new installation's page: %d → %s", res.StatusCode, res.Header.Get("Location"))
	}
	if r := empty.CreateCompany(cfg); !r.OK {
		t.Fatalf("a new installation could not build its factory: %v", r.Errors)
	}
	if r := empty.CreateCompany([]byte(strings.Replace(string(cfg), `"xinghe"`, `"other"`, 1))); r.OK {
		t.Error("an installation built a second company")
	}
}
