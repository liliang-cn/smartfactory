package web

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"smartfactory/internal/audit"
	"smartfactory/internal/auth"
	"smartfactory/internal/checks"
	"smartfactory/internal/engine"
	"smartfactory/internal/guard"
	"smartfactory/internal/knowledge"
	"smartfactory/internal/sfptest"
	"smartfactory/internal/tags"
)

// What happens to a machine reaches the knowledge base on its own: a patrol filled in out of
// spec on RF-101 and a command an MCP client sent it are in the graph a few seconds later, linked
// to the station, and "RF-101 点检" finds the check first; the answers hold to the protocol.
func TestKnowledgeEvents(t *testing.T) {
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
	trail, _ := audit.Open(t.TempDir(), log)
	t.Cleanup(func() { trail.Close() })
	hub := tags.NewHub()
	manual, err := checks.Open(t.TempDir(), eng, hub, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { manual.Close() })
	cmds, err := guard.Open(t.TempDir(), eng, hub, trail, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmds.Close() })
	kb, err := knowledge.Open(t.TempDir(), eng, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(kb.Close)
	if err := kb.SyncProcess(ctx, "huaduan"); err != nil {
		t.Fatal(err)
	}
	kb.Watch(knowledge.Sources{Checks: manual, Guard: cmds, Audit: trail})
	go kb.RunEvents(ctx)

	member, mk := auth.NewToken("小陈", auth.Member, []string{"huaduan"}, "")
	srv := &Server{Hub: hub, Engine: eng, Keys: keyring(t, "", mk), Audit: trail, Log: log, Knowledge: kb, Checks: manual, Guard: cmds}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	get := func(path string) []byte {
		t.Helper()
		req, _ := http.NewRequest("GET", ts.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+member)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		if res.StatusCode != 200 {
			t.Fatalf("%s: %d %s", path, res.StatusCode, b)
		}
		return b
	}

	form := checks.Templates()[0]
	form.Plant, form.Assets = "p1", []string{"RF-101"}
	f, err := manual.SaveForm("huaduan", form, "厂长")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manual.Submit("huaduan", checks.Input{Form: f.ID, Plant: "p1", Asset: "RF-101", Values: map[string]any{
		"oil": "偏低", "noise": "否", "leak": "否", "gauge_temp": 92.5, "guard": "正常"}}, checks.Author{Name: "张工", Via: "page"}); err != nil {
		t.Fatal(err)
	}
	if v := cmds.Send(guard.Origin{Who: auth.Who{Name: "claude"}, Via: "mcp", By: "mcp", AI: true}, guard.Command{Company: "huaduan", Plant: "p1", Asset: "RF-101", Cmd: "stop"}); v.Outcome != guard.Sent {
		t.Fatalf("the command: %+v", v)
	}
	var st struct{ Kinds map[string]int }
	for deadline := time.Now().Add(20 * time.Second); ; {
		b := get("/api/knowledge/huaduan")
		std.Check("KnowledgeOverview", b)
		json.Unmarshal(b, &st)
		if st.Kinds["check"] == 2 && st.Kinds["command"] == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not in the graph: %s", b)
		}
		time.Sleep(200 * time.Millisecond)
	}

	b := get("/api/knowledge/huaduan/search?q=" + url.QueryEscape("RF-101 点检") + "&limit=6")
	std.Check("KnowledgeAnswer", b)
	var a knowledge.Answer
	json.Unmarshal(b, &a)
	if len(a.Passages) == 0 || a.Passages[0].From != "check" || !strings.Contains(a.Passages[0].Text, "温度表读数 92.5 °C，超上限 80") {
		t.Fatalf("RF-101's checks: %s", b)
	}
	b = get("/api/knowledge/huaduan/search?q=" + url.QueryEscape("AI 对 RF-101 发过什么指令"))
	json.Unmarshal(b, &a)
	if len(a.Passages) == 0 || a.Passages[0].From != "command" || !strings.Contains(a.Passages[0].Text, "claude") {
		t.Fatalf("RF-101's commands: %s", b)
	}
	b = get("/api/knowledge/huaduan/entity?name=RF-101")
	std.Check("EntityAnswer", b)
	var e struct{ Entity knowledge.Entity }
	json.Unmarshal(b, &e)
	var check string
	for _, l := range e.Entity.Links {
		if l.Kind == "check" && strings.Contains(l.Name, "异常") {
			check = l.Name
		}
	}
	if check == "" {
		t.Fatalf("RF-101's links: %s", b)
	}
	b = get("/api/knowledge/huaduan/entity?name=" + url.QueryEscape(check))
	std.Check("EntityAnswer", b)
	json.Unmarshal(b, &e)
	if !slices.Contains(e.Entity.Detail, knowledge.Fact{Label: "油位", Value: "偏低", Bad: true}) || e.Entity.Kind != "check" {
		t.Errorf("the check's card: %s", b)
	}
	std.Check("KnowledgeGraph", get("/api/knowledge/huaduan/graph"))
}
