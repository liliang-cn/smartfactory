package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"

	"smartfactory/internal/agents"
	"smartfactory/internal/engine"
	"smartfactory/internal/sfptest"
	"smartfactory/internal/tags"
)

// Everything the server sends a browser conforms to the standard, and the server reads every
// message the standard lets a browser send, field for field.
func TestProtocol(t *testing.T) {
	if _, err := os.Stat("../engine/js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	std := sfptest.Load(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng, err := engine.New(log)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go eng.Run(ctx)

	hub := tags.NewHub()
	hub.Source("plc", "modbus", "tcp://127.0.0.1:41502", 1)
	hub.Source("broker-client", "mqtt", "tcp://127.0.0.1:41883", 0)
	hub.Publish(tags.Tag{Company: "huaduan", Plant: "p1", Asset: "HP-201", Field: "temp", Value: 79.5, Source: "plc", At: time.Now()})
	const token = "operator-token"
	srv := &Server{
		Hub: hub, Engine: eng, Token: token, Version: "test", Log: log,
		Static:     fstest.MapFS{"index.html": {Data: []byte("<!doctype html>")}},
		Advisors:   map[string]string{"huaduan": "https://advisor.example"},
		AdvisorAPI: map[string]Advisor{},
	}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	for _, c := range []struct{ path, def string }{
		{"/api/health", "Health"},
		{"/api/config/directory", "Directory"},
		{"/api/tags", "Tags"},
		{"/api/tags?company=huaduan&plant=p1", "Tags"},
		{"/api/advisors", "Advisors"},
		{"/api/config/company/nobody", "ErrorBody"},
		{"/api/advisor/huaduan/metrics", "ErrorBody"},
		{"/api/devices/huaduan/p1", "DeviceMap"},
		{"/api/devices/huaduan/nowhere", "ErrorBody"},
	} {
		res, err := http.Get(ts.URL + c.path)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if ct := res.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("%s: content type %q", c.path, ct)
		}
		std.Check(c.def, b)
	}

	// The live channel, as an operator: every kind of server message turns up.
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/live?token="+token, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer ws.CloseNow()
	ws.SetReadLimit(16 << 20)
	sent := []string{
		`{"type":"watch","company":"huaduan","plant":"p1"}`,
		`{"type":"cmd","plant":"p1","asset":"HP-101","cmd":"speed","arg":1.1,"o":"tab1"}`,
		`{"type":"hist","plant":"p1","asset":"HP-201"}`,
		`{"type":"time","scale":2}`,
		`{"type":"plant","plant":"p2"}`,
		`{"type":"watch","company":"huaduan","plant":"all"}`,
	}
	for _, m := range sent {
		std.Check("ClientMessage", []byte(m))
		if err := ws.Write(ctx, websocket.MessageText, []byte(m)); err != nil {
			t.Fatal(err)
		}
	}
	want := map[string]bool{"hello": false, "frame": false, "result": false, "hist": false, "cmd": false}
	plants := map[string]bool{}
	deadline, stop := context.WithTimeout(ctx, 10*time.Second)
	defer stop()
	for slices.Contains(slices.Collect(func(yield func(bool) bool) {
		for _, v := range want {
			if !yield(v) {
				return
			}
		}
	}), false) || len(plants) < 3 {
		_, b, err := ws.Read(deadline)
		if err != nil {
			t.Fatalf("still waiting for %v (plants %v): %v", want, plants, err)
		}
		var m struct {
			Type     string `json:"type"`
			Protocol int    `json:"protocol"`
			Data     struct {
				Plants map[string]json.RawMessage `json:"plants"`
			} `json:"data"`
		}
		json.Unmarshal(b, &m)
		std.Check("ServerMessage", b)
		want[m.Type] = true
		if m.Type == "hello" && m.Protocol != Protocol {
			t.Errorf("hello says protocol %d, server is %d", m.Protocol, Protocol)
		}
		for p := range m.Data.Plants {
			plants[p] = true
		}
	}

	// The server's reading of browser messages knows every field the standard defines, and no others.
	var defined []string
	for _, v := range std.Root.Defs["ClientMessage"].OneOf {
		for k := range v.Properties {
			if !slices.Contains(defined, k) {
				defined = append(defined, k)
			}
		}
	}
	var read []string
	for f := range reflect.TypeFor[clientMsg]().Fields() {
		read = append(read, strings.Split(f.Tag.Get("json"), ",")[0])
	}
	slices.Sort(defined)
	slices.Sort(read)
	if !slices.Equal(defined, read) {
		t.Errorf("browser message fields: standard %v, server reads %v", defined, read)
	}
	for _, m := range sent {
		d := json.NewDecoder(bytes.NewReader([]byte(m)))
		d.DisallowUnknownFields()
		if err := d.Decode(&clientMsg{}); err != nil {
			t.Errorf("%s: %v", m, err)
		}
	}
}

// The assistants' HTTP surface conforms to the standard: what a viewer reads, what an operator
// sets up, and each kind of event an answer streams.
func TestAssistantsProtocol(t *testing.T) {
	if _, err := os.Stat("../engine/js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	t.Setenv("LLM_API_KEY", "") // no model: asking is refused, everything else works
	t.Setenv("AGENTGO_HOME", t.TempDir())
	std := sfptest.Load(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng, err := engine.New(log)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go eng.Run(ctx)
	ag, err := agents.New(t.TempDir(), eng, log)
	if err != nil {
		t.Fatal(err)
	}
	defer ag.Close()
	const token = "operator-token"
	ts := httptest.NewServer((&Server{Hub: tags.NewHub(), Engine: eng, Token: token, Log: log, Agents: ag,
		Static: fstest.MapFS{"index.html": {Data: []byte("<!doctype html>")}}}).Handler())
	defer ts.Close()

	call := func(method, path, body string, op bool, want int, def string) []byte {
		t.Helper()
		if body != "" {
			for _, d := range []string{"AssistantRequest", "AssistantAsk", "DocUpload", "TicketPatch"} {
				if strings.Contains(def, d) {
					std.Check(d, []byte(body))
				}
			}
		}
		req, _ := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
		req.Header.Set("X-SF-Visitor", "test-browser")
		if op {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != want {
			t.Fatalf("%s %s: %d, want %d: %s", method, path, res.StatusCode, want, b)
		}
		std.Check(strings.Fields(def)[0], b)
		return b
	}
	var list struct {
		Agents []struct{ ID, Kind string } `json:"agents"`
	}
	json.Unmarshal(call("GET", "/api/agents/huaduan", "", false, 200, "Assistants"), &list)
	if len(list.Agents) != 3 {
		t.Fatalf("huaduan starts with %d assistants, want 3", len(list.Agents))
	}
	call("GET", "/api/agents/nobody", "", false, 404, "ErrorBody")
	var docs []struct{ ID string }
	json.Unmarshal(call("GET", "/api/library/huaduan/docs", "", false, 200, "Docs"), &docs)
	if len(docs) == 0 {
		t.Fatal("huaduan has no documents")
	}
	call("GET", "/api/library/huaduan/docs/"+docs[0].ID, "", false, 200, "DocView")
	call("GET", "/api/library/huaduan/docs/nope", "", false, 404, "ErrorBody")
	call("POST", "/api/library/huaduan/docs", `{"title":"试验","body":"# 一\n\n内容"}`, false, 403, "ErrorBody DocUpload")
	call("POST", "/api/library/huaduan/docs", `{"title":"试验","body":"# 一\n\n内容"}`, true, 200, "Doc DocUpload")
	call("GET", "/api/library/huaduan/notes", "", false, 200, "Notes")
	var tickets []struct{ ID string }
	json.Unmarshal(call("GET", "/api/library/huaduan/tickets", "", false, 200, "Tickets"), &tickets)
	call("PATCH", "/api/library/huaduan/tickets/"+tickets[0].ID, `{"status":"已关闭"}`, true, 200, "Ticket TicketPatch")

	made := call("POST", "/api/agents/huaduan", `{"kind":"docs","name":"标准助手","instructions":"只看国标"}`, true, 200, "Assistant AssistantRequest")
	var a struct{ ID string }
	json.Unmarshal(made, &a)
	call("POST", "/api/agents/huaduan", `{"kind":"docs","name":"标准助手"}`, false, 403, "ErrorBody AssistantRequest")
	call("PUT", "/api/agents/huaduan/"+a.ID, `{"name":"国标助手","instructions":""}`, true, 200, "Assistant AssistantRequest")
	call("GET", "/api/agents/huaduan/"+a.ID+"/conversations", "", false, 200, "Conversations")
	call("POST", "/api/agents/huaduan/"+a.ID+"/ask", `{"text":"有哪些标准？"}`, false, 503, "ErrorBody AssistantAsk")

	// a conversation as it is kept, read back by its owner only
	conv, err := ag.Store.CreateConversation("huaduan", a.ID, "visitor:test-browser", "有哪些标准？")
	if err != nil {
		t.Fatal(err)
	}
	ag.Store.AddMessage(conv.ID, agents.Message{Role: "user", Text: "有哪些标准？", Status: "done"})
	ag.Store.AddMessage(conv.ID, agents.Message{Role: "assistant", Text: "一份。", Status: "done",
		Steps: []agents.Step{{Tool: "list_docs", Args: map[string]any{}, Result: []any{map[string]any{"title": "试验"}}}}})
	call("GET", "/api/agents/huaduan/"+a.ID+"/conversations", "", false, 200, "Conversations")
	call("GET", "/api/agents/huaduan/"+a.ID+"/conversations/"+conv.ID, "", false, 200, "ConversationView")
	call("GET", "/api/agents/huaduan/"+a.ID+"/conversations/"+conv.ID, "", true, 404, "ErrorBody")
	call("DELETE", "/api/agents/huaduan/"+a.ID+"/conversations/"+conv.ID, "", false, 200, "Done")
	call("DELETE", "/api/agents/huaduan/"+a.ID, "", true, 200, "Done")

	for _, e := range []agents.Event{
		{Kind: "conversation", ID: "c1", Title: "有哪些标准？"}, {Kind: "thinking"},
		{Kind: "tool_call", Tool: "search_docs", Args: map[string]any{"query": "模温"}},
		{Kind: "tool_result", Tool: "search_docs", Result: []any{}}, {Kind: "delta", Text: "模温"}, {Kind: "reset"},
		{Kind: "complete", Text: "200～300 ℃"}, {Kind: "complete"}, {Kind: "error", Text: "服务器没有配置大模型"},
	} {
		b, _ := json.Marshal(e)
		std.Check("AssistantEvent", b)
	}
}
