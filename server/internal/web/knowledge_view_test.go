package web

import (
	"bufio"
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

	"smartfactory/internal/audit"
	"smartfactory/internal/auth"
	"smartfactory/internal/engine"
	"smartfactory/internal/knowledge"
	"smartfactory/internal/tags"
)

// The knowledge base in CortexDB's live view, mounted in the console's API: a company's
// administrator opens the page and the graph it reads under the same prefix, its ontology page
// draws the company's own classes, a search lights the things it read on an open page, and a
// member, another company's administrator and a visitor are refused.
func TestKnowledgeView(t *testing.T) {
	if _, err := os.Stat("../engine/js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	t.Setenv("LLM_API_KEY", "")
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
	kb, err := knowledge.Open(t.TempDir(), eng, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(kb.Close)
	if err := kb.SyncProcess(ctx, "huaduan"); err != nil {
		t.Fatal(err)
	}
	trail, _ := audit.Open(t.TempDir(), log)
	t.Cleanup(func() { trail.Close() })

	admin, ak := auth.NewToken("厂长", auth.Admin, []string{"huaduan"}, "")
	member, mk := auth.NewToken("计划员", auth.Member, []string{"huaduan"}, "")
	outsider, ok := auth.NewToken("别家", auth.Admin, []string{other}, "")
	srv := &Server{Hub: tags.NewHub(), Engine: eng, Keys: keyring(t, "", ak, mk, ok), Audit: trail, Log: log, Knowledge: kb}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	get := func(token, path string) (int, string) {
		t.Helper()
		req, _ := http.NewRequest("GET", ts.URL+path, nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(b)
	}

	if code, body := get(admin, "/api/knowledge/huaduan/live/"); code != 200 || !strings.Contains(body, "<html") {
		t.Fatalf("the page: %d %.200s", code, body)
	}
	code, body := get(admin, "/api/knowledge/huaduan/live/api/graph")
	if code != 200 {
		t.Fatalf("the graph: %d %.200s", code, body)
	}
	var g struct {
		Nodes []struct{ ID, Type string } `json:"nodes"`
	}
	json.Unmarshal([]byte(body), &g)
	machines := 0
	for _, n := range g.Nodes {
		if n.Type == "machine" {
			machines++
		}
	}
	if machines == 0 {
		t.Fatalf("the view drew no machines of the company: %d nodes", len(g.Nodes))
	}

	code, body = get(admin, "/api/knowledge/huaduan/live/api/ontology")
	var onto struct {
		State       string `json:"state"`
		ObjectTypes []struct {
			APIName string `json:"api_name"`
		} `json:"object_types"`
	}
	json.Unmarshal([]byte(body), &onto)
	if code != 200 || onto.State != "live" || len(onto.ObjectTypes) == 0 {
		t.Fatalf("the ontology page has not the company's classes: %d %.300s", code, body)
	}

	// CortexDB's own bookkeeping is left out of it
	for _, p := range []string{"recalls", "library", "api/recalls"} {
		if code, _ := get(admin, "/api/knowledge/huaduan/live/"+p); code != 404 {
			t.Errorf("the view still serves %s: %d", p, code)
		}
	}

	for _, who := range []struct{ name, token string }{{"a member", member}, {"another company's administrator", outsider}, {"a visitor", ""}} {
		if code, _ := get(who.token, "/api/knowledge/huaduan/live/api/graph"); code != 401 && code != 403 {
			t.Errorf("%s read the view: %d", who.name, code)
		}
	}

	// an open page sees what a search read
	sctx, stop := context.WithTimeout(ctx, 20*time.Second)
	defer stop()
	req, _ := http.NewRequestWithContext(sctx, "GET", ts.URL+"/api/knowledge/huaduan/live/api/stream", nil)
	req.Header.Set("Authorization", "Bearer "+admin)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	lit := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(res.Body)
		sc.Buffer(make([]byte, 1<<20), 8<<20)
		for sc.Scan() {
			if line := sc.Text(); strings.HasPrefix(line, "data: ") && strings.Contains(line, "search_knowledge") {
				lit <- line
				return
			}
		}
	}()
	time.Sleep(300 * time.Millisecond)
	if _, err := kb.Search(ctx, "huaduan", "辊锻机的工艺", nil, nil, 4); err != nil {
		t.Fatal(err)
	}
	select {
	case line := <-lit:
		if !strings.Contains(line, `"nodes":[`) || !strings.Contains(line, "entity:") {
			t.Fatalf("the search lit nothing by id: %.400s", line)
		}
	case <-sctx.Done():
		t.Fatal("an open page never saw the search")
	}
}
