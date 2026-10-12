package web

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"smartfactory/internal/audit"
	"smartfactory/internal/auth"
	"smartfactory/internal/checks"
	"smartfactory/internal/config"
	"smartfactory/internal/engine"
	"smartfactory/internal/mcptools"
	"smartfactory/internal/series"
	"smartfactory/internal/sfptest"
	"smartfactory/internal/tags"
)

// Forms filled in by hand, through the API the phone page and the console use: an administrator
// sets a form up from a template (members cannot); an operator fills it in on a machine, with a
// photo and a value out of spec (members cannot); the phone resends it and it is kept once; a
// round from a phone that was offline lands at the time it was taken in the machine's stored
// readings; the round counts as done, the history exports as CSV, the activity log has every
// value under the author's name, MCP clients see it; every answer holds to the protocol.
func TestChecks(t *testing.T) {
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
	eng.Single()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); <-eng.Done })
	go eng.Run(ctx)
	var cfg map[string]any
	json.Unmarshal([]byte(eng.BuiltinTemplate("huaduan")), &cfg)
	cfg["id"], cfg["name"], cfg["short"] = "jianghe", "江河锻造", "江河"
	b, _ := json.Marshal(cfg)
	if r := eng.CreateCompany(b); !r.OK {
		t.Fatal(r.Errors)
	}
	store, err := series.Open(t.TempDir(), series.RetentionFrom(config.Series{}), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	hub := tags.NewHub()
	hub.Check = (&series.Validator{Machines: eng.Machines}).Check
	hub.Tap = store.Add
	trail, _ := audit.Open(t.TempDir(), log)
	t.Cleanup(func() { trail.Close() })
	svc, err := checks.Open(t.TempDir(), eng, hub, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { svc.Close() })

	op, opk := auth.NewToken("张工", auth.Operator, []string{"jianghe"}, "")
	member, mk := auth.NewToken("小李", auth.Member, []string{"jianghe"}, "")
	admin, adk := auth.NewToken("厂长", auth.Admin, []string{"jianghe"}, "")
	ms := mcptools.New(hub, eng, "test", trail, nil)
	mcptools.AddChecks(ms, svc)
	srv := &Server{Hub: hub, Engine: eng, Keys: keyring(t, "", opk, mk, adk), Audit: trail, Log: log, Series: store, Checks: svc,
		MCP: mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return ms }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, DisableLocalhostProtection: true})}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	do := func(token, method, path string, body any) (int, []byte, http.Header) {
		t.Helper()
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
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
		return res.StatusCode, b, res.Header
	}

	// a press with a quality measure
	var plant, asset string
	var spec *engine.Spec
	for _, p := range eng.Stations("jianghe") {
		for _, l := range p.Lines {
			for _, st := range l.Stations {
				if spec == nil && st.Measure != nil && st.Measure.Hi != nil {
					plant, asset, spec = p.ID, st.ID, st.Measure
				}
			}
		}
	}

	if code, _, _ := do("", "GET", "/api/checks/jianghe", nil); code != 401 {
		t.Errorf("a visitor reads the forms: %d", code)
	}
	code, b, _ := do(member, "GET", "/api/checks/jianghe/templates", nil)
	if code != 200 {
		t.Fatalf("templates: %d %s", code, b)
	}
	std.Check("CheckForms", b)
	var tpl []checks.Form
	json.Unmarshal(b, &tpl)
	first := tpl[1]
	first.Plant, first.Assets = plant, []string{asset}
	if code, _, _ := do(member, "POST", "/api/checks/jianghe/forms", first); code != 403 {
		t.Errorf("a member set a form up: %d", code)
	}
	bad := first
	bad.Fields = append([]checks.Field{{Key: "x", Label: "", Kind: "dial"}}, first.Fields...)
	if code, b, _ := do(admin, "POST", "/api/checks/jianghe/forms", bad); code != 422 || !strings.Contains(string(b), "第 1 项没有名称") {
		t.Errorf("a bad form: %d %s", code, b)
	} else {
		std.Check("ConfigProblem", b)
	}
	code, b, _ = do(admin, "POST", "/api/checks/jianghe/forms", first)
	if code != 200 {
		t.Fatalf("set up: %d %s", code, b)
	}
	std.Check("CheckForm", b)
	var form checks.Form
	json.Unmarshal(b, &form)

	code, b, _ = do(member, "GET", "/api/checks/jianghe", nil)
	if code != 200 {
		t.Fatalf("overview: %d %s", code, b)
	}
	std.Check("ChecksOverview", b)
	var ov struct {
		Machines []checks.Machine
		Operator bool
	}
	json.Unmarshal(b, &ov)
	if len(ov.Machines) != 1 || ov.Machines[0].Asset != asset || ov.Operator {
		t.Errorf("overview for a member: %+v", ov)
	}

	// filled in on the phone: a unit over its spec, with a photo
	var pic bytes.Buffer
	png.Encode(&pic, image.NewGray(image.Rect(0, 0, 4, 4)))
	photo := "data:image/png;base64," + base64.StdEncoding.EncodeToString(pic.Bytes())
	over := *spec.Hi + 0.1
	in := map[string]any{"id": "ph-0001", "form": form.ID, "plant": plant, "asset": asset, "taken": time.Now().Add(-10 * time.Second).Format(time.RFC3339Nano),
		"values": map[string]any{"size": over, "look": "合格", "verdict": "合格", "photo": photo, "note": "毛刺略多"}}
	if code, _, _ := do(member, "POST", "/api/checks/jianghe/submissions", in); code != 403 {
		t.Errorf("a member filled a form in: %d", code)
	}
	code, b, _ = do(op, "POST", "/api/checks/jianghe/submissions", in)
	if code != 200 {
		t.Fatalf("submit: %d %s", code, b)
	}
	std.Check("CheckSubmission", b)
	var sub checks.Submission
	json.Unmarshal(b, &sub)
	if sub.Bad != 1 || sub.By != "张工" || sub.Late || len(sub.Values) != 5 {
		t.Errorf("submitted: %s", b)
	}
	var photoID string
	for _, v := range sub.Values {
		if v.Kind == checks.Photo {
			photoID = v.Text
		}
	}
	if code, b, h := do(member, "GET", "/api/checks/jianghe/photos/"+photoID, nil); code != 200 || h.Get("Content-Type") != "image/png" || !bytes.Equal(b, pic.Bytes()) {
		t.Errorf("the photo: %d %s", code, h.Get("Content-Type"))
	}
	if code, b, _ := do(op, "POST", "/api/checks/jianghe/submissions", in); code != 200 || !strings.Contains(string(b), `"duplicate": true`) {
		t.Errorf("sent again: %d %s", code, b)
	}

	// a round from a phone that was offline for three hours
	then := time.Now().Add(-3 * time.Hour).Truncate(time.Millisecond)
	code, b, _ = do(op, "POST", "/api/checks/jianghe/submissions", map[string]any{"id": "ph-0002", "form": form.ID, "plant": plant, "asset": asset,
		"taken": then.Format(time.RFC3339Nano), "values": map[string]any{"size": spec.Target, "look": "合格", "verdict": "合格"}})
	if code != 200 || !strings.Contains(string(b), `"late": true`) {
		t.Fatalf("offline round: %d %s", code, b)
	}
	store.Flush()
	_, b, _ = do(member, "GET", fmt.Sprintf("/api/series/jianghe/%s/%s?field=measure&step=raw&from=%d", plant, asset, then.Add(-time.Minute).UnixMilli()), nil)
	std.Check("SeriesAnswer", b)
	if !strings.Contains(string(b), fmt.Sprint(then.UnixMilli())) {
		t.Errorf("the offline reading is not stored at its time %d: %s", then.UnixMilli(), b)
	}

	code, b, _ = do(member, "GET", "/api/checks/jianghe/machines/"+plant+"/"+asset, nil)
	if code != 200 {
		t.Fatalf("machine: %d %s", code, b)
	}
	std.Check("MachineChecks", b)
	var mc struct {
		Machine checks.Machine
		Last    []checks.Submission
	}
	json.Unmarshal(b, &mc)
	if mc.Machine.Rounds[0].Status != "done" || len(mc.Last) != 1 || mc.Last[0].ID != "ph-0001" {
		t.Errorf("the press's page: %s", b)
	}
	if code, _, _ := do(member, "GET", "/api/checks/jianghe/machines/p1/NOPE", nil); code != 404 {
		t.Errorf("a machine no form is for: %d", code)
	}
	code, b, _ = do(member, "GET", "/api/checks/jianghe/rounds?status=done", nil)
	std.Check("CheckRounds", b)
	if code != 200 || !strings.Contains(string(b), asset) {
		t.Errorf("done rounds: %d %s", code, b)
	}
	if code, _, _ := do(member, "GET", "/api/checks/jianghe/rounds?status=late", nil); code != 400 {
		t.Errorf("a status that is not one: %d", code)
	}

	code, b, _ = do(member, "GET", "/api/checks/jianghe/submissions?bad=1", nil)
	std.Check("CheckSubmissions", b)
	if code != 200 || strings.Count(string(b), `"id": "ph-`) != 1 {
		t.Errorf("abnormal submissions: %d %s", code, b)
	}
	code, b, h := do(member, "GET", "/api/checks/jianghe/submissions?format=csv&asset="+asset, nil)
	if code != 200 || !strings.HasPrefix(h.Get("Content-Type"), "text/csv") || !bytes.HasPrefix(b, []byte("\xef\xbb\xbf记录编号")) || !strings.Contains(string(b), "ph-0002") {
		t.Errorf("csv: %d %s\n%s", code, h, b)
	}

	// the activity log: who wrote what
	_, b, _ = do(op, "GET", "/api/audit/jianghe", nil)
	if !strings.Contains(string(b), `"action": "check.submit"`) || !strings.Contains(string(b), "超上限") || !strings.Contains(string(b), "手机当时没网") || !strings.Contains(string(b), `"action": "check.form"`) {
		t.Errorf("activity: %s", b)
	}

	// MCP clients see the forms and what was written
	cl := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil)
	s, err := cl.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: ts.URL + "/mcp", HTTPClient: &http.Client{Transport: bearer{member}}, DisableStandaloneSSE: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for _, c := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"check_forms", map[string]any{"company": "jianghe"}, form.ID},
		{"check_rounds", map[string]any{"company": "jianghe", "status": "done"}, asset},
		{"check_results", map[string]any{"company": "jianghe", "bad": true}, "ph-0001"},
	} {
		tool, want := c.tool, c.want
		res, err := s.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: c.args})
		if err != nil || res.IsError {
			t.Fatalf("%s: %v %v", tool, err, res)
		}
		b, _ := json.Marshal(res.StructuredContent)
		if !strings.Contains(string(b), want) {
			t.Errorf("%s: %s", tool, b)
		}
	}

	if code, b, h := do("", "GET", "/api/qr?text="+ts.URL+"/check?company=jianghe", nil); code != 200 || h.Get("Content-Type") != "image/svg+xml" || !bytes.HasPrefix(b, []byte("<svg")) {
		t.Errorf("qr: %d %s", code, h)
	}
	if code, _, _ := do(admin, "DELETE", "/api/checks/jianghe/forms/"+form.ID, nil); code != 200 {
		t.Errorf("delete: %d", code)
	}
	if code, b, _ := do(op, "POST", "/api/checks/jianghe/submissions", map[string]any{"form": "F-NONE", "plant": plant, "asset": asset, "values": map[string]any{"size": 1}}); code != 422 {
		t.Errorf("a form that is not there: %d %s", code, b)
	}
}
