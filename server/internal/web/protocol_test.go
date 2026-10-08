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
	"github.com/google/jsonschema-go/jsonschema"

	"smartfactory/internal/engine"
	"smartfactory/internal/tags"
)

// The protocol standard, as written by `npm run schema` from src/protocol/sfp.ts.
const schemaFile = "../../../protocol/sfp.schema.json"

type standard struct {
	t    *testing.T
	root jsonschema.Schema
}

func loadStandard(t *testing.T) *standard {
	t.Helper()
	b, err := os.ReadFile(schemaFile)
	if err != nil {
		t.Fatal(err)
	}
	s := &standard{t: t}
	if err := json.Unmarshal(b, &s.root); err != nil {
		t.Fatal(err)
	}
	return s
}

// check validates one message or response body against the definition named def.
func (s *standard) check(def string, raw []byte) {
	s.t.Helper()
	root := s.root
	root.Ref = "#/$defs/" + def
	r, err := root.Resolve(nil)
	if err != nil {
		s.t.Fatalf("%s: %v", def, err)
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		s.t.Fatalf("%s: not JSON: %v\n%.300s", def, err, raw)
	}
	if err := r.Validate(v); err != nil {
		s.t.Errorf("%s does not conform: %v\n%.300s", def, err, raw)
	}
}

// Everything the server sends a browser conforms to the standard, and the server reads every
// message the standard lets a browser send, field for field.
func TestProtocol(t *testing.T) {
	if _, err := os.Stat("../engine/js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	std := loadStandard(t)
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
		std.check(c.def, b)
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
		std.check("ClientMessage", []byte(m))
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
		std.check("ServerMessage", b)
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
	for _, v := range std.root.Defs["ClientMessage"].OneOf {
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
