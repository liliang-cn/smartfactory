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
	"time"

	"smartfactory/internal/auth"
	"smartfactory/internal/engine"
	"smartfactory/internal/health"
	"smartfactory/internal/tags"
)

// 运行状况: only an administrator reads it; any twin page can report how it is doing, and what
// it reports shows up summed; a report that is not plausible is refused.
func TestHealth(t *testing.T) {
	if _, err := os.Stat("../engine/js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng, err := engine.New(log)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); <-eng.Done })
	go eng.Run(ctx)
	admin, ak := auth.NewToken("李工", auth.Admin, nil, "")
	member, mk := auth.NewToken("小陈", auth.Member, nil, "")
	h := health.New(t.TempDir(), health.Probes{StepMs: func() float64 { return eng.StepCost().Seconds() * 1000 }})
	h.Take(time.Now())
	srv := httptest.NewServer((&Server{Hub: tags.NewHub(), Engine: eng, Keys: keyring(t, "", ak, mk), Log: log, Health: h,
		Model: func() (string, bool) { return "claude-opus-5-5", true }}).Handler())
	defer srv.Close()

	get := func(token string) (int, map[string]any) {
		req, _ := http.NewRequest("GET", srv.URL+"/api/admin/health", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		var out map[string]any
		json.NewDecoder(res.Body).Decode(&out)
		return res.StatusCode, out
	}
	if code, _ := get(""); code != http.StatusUnauthorized {
		t.Errorf("no token: %d", code)
	}
	if code, _ := get(member); code != http.StatusForbidden {
		t.Errorf("member: %d", code)
	}
	report := func(body string) int {
		res, err := http.Post(srv.URL+"/api/health/browser", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		return res.StatusCode
	}
	if c := report(`{"id":"tab-12345678","fps":58,"heapMB":45,"width":1920,"height":900,"dpr":1}`); c != http.StatusNoContent {
		t.Errorf("report: %d", c)
	}
	if c := report(`{"id":"tab-87654321","fps":12,"mobile":true}`); c != http.StatusNoContent {
		t.Errorf("report: %d", c)
	}
	if c := report(`{"id":"x","fps":99999}`); c != http.StatusBadRequest {
		t.Errorf("implausible report: %d", c)
	}
	code, out := get(admin)
	if code != http.StatusOK {
		t.Fatalf("admin: %d %v", code, out)
	}
	b := out["browsers"].(map[string]any)
	if b["count"].(float64) != 2 || b["mobile"].(float64) != 1 || len(b["slow"].([]any)) != 1 {
		t.Errorf("browsers %v", b)
	}
	if m := out["model"].(map[string]any); m["name"] != "claude-opus-5-5" {
		t.Errorf("model %v", m)
	}
	if p := out["process"].(map[string]any); p["rss"].(float64) <= 0 {
		t.Errorf("process %v", p)
	}
	e := out["engine"].(map[string]any)
	if e["budgetMs"].(float64) != 100 || e["machines"].(float64) == 0 || len(e["plants"].([]any)) == 0 {
		t.Errorf("engine %v", e)
	}
	if len(out["samples"].([]any)) != 1 {
		t.Errorf("samples %v", out["samples"])
	}
}
