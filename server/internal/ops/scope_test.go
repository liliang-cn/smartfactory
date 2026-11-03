package ops

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"

	"smartfactory/internal/audit"
	"smartfactory/internal/auth"
	"smartfactory/internal/engine"
	"smartfactory/internal/scoped"
)

const scopeCompany = `{
  "id": "testco", "name": "测试锻造", "short": "测试", "industry": "forging",
  "plants": [{"id": "p1", "name": "一厂", "hall": "锻造车间", "lines": [
    {"name": "曲轴线", "product": "crankshaft", "route": "hot-die-crank"},
    {"name": "连杆线", "product": "conrod", "route": "hot-die-conrod"}
  ]}]
}`

func scopeOps(t *testing.T) (*Ops, *audit.Log) {
	t.Helper()
	if _, err := os.Stat("../engine/js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng, err := engine.NewOffline(log)
	if err != nil {
		t.Fatal(err)
	}
	eng.Dir = t.TempDir()
	trail, err := audit.Open(t.TempDir(), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { trail.Close() })
	o := &Ops{Engine: eng, Audit: trail}
	if r := eng.CreateCompany([]byte(scopeCompany)); !r.OK {
		t.Fatal(r.Errors)
	}
	return o, trail
}

var admin = auth.Who{Name: "厂长", Role: auth.Admin, Key: "k1"}

// A change to one line goes through the guard, is saved like a builder save, is in the activity
// log, keeps the earlier config, and is undone back to it; a second undo has nothing left.
func TestScopeApplyAndUndo(t *testing.T) {
	o, trail := scopeOps(t)
	line := scoped.Node{Kind: "line", Plant: "p1", Line: "L1"}
	v, err := o.Scope(admin, "testco", line)
	if err != nil {
		t.Fatal(err)
	}
	if v.Scope.Title != "产线「曲轴线」" || v.Versions != 0 {
		t.Fatalf("view: %+v", v.Scope)
	}
	before := o.Engine.CompanyConfig("testco")

	// out of bounds: refused as a whole, naming what it touched, and nothing saved
	_, err = o.ScopeApply(admin, "page", "testco", ScopeRequest{Node: line, Base: v.Base, Changes: []scoped.Change{
		{Op: "set", Path: "plants/0/lines/0/steps/3/cycle", Value: 40.0},
		{Op: "set", Path: "plants/0/lines/1/steps/3/cycle", Value: 40.0},
	}})
	var oe *Error
	if !errors.As(err, &oe) || oe.Code != 422 || oe.Where != "scope" || !strings.Contains(oe.Msg, "产线「连杆线」") {
		t.Fatalf("guard: %v", err)
	}
	if o.Engine.CompanyConfig("testco") != before {
		t.Fatal("a refused change was saved")
	}

	// the form's way: the whole config with the line changed
	cfg := v.Model.Config
	steps := cfg["plants"].([]any)[0].(map[string]any)["lines"].([]any)[0].(map[string]any)["steps"].([]any)
	steps = append(steps, map[string]any{"machine": "vision", "name": "在线检测"})
	cfg["plants"].([]any)[0].(map[string]any)["lines"].([]any)[0].(map[string]any)["steps"] = steps
	pv, err := o.ScopePreview(admin, "testco", ScopeRequest{Node: line, Base: v.Base, Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	if len(pv.Preview.Tags) != 0 || len(pv.Preview.Added) != 1 || pv.Preview.Added[0] != "VI-101" {
		t.Fatalf("preview: %+v", pv.Preview)
	}
	if o.Engine.CompanyConfig("testco") != before {
		t.Fatal("a preview saved")
	}
	r, err := o.ScopeApply(admin, "page", "testco", ScopeRequest{Node: line, Base: v.Base, Config: cfg})
	if err != nil || !r.Saved {
		t.Fatal(r, err)
	}
	var model struct {
		Plants []struct {
			Lines []struct {
				Stations []struct{ ID string } `json:"stations"`
			} `json:"lines"`
		} `json:"plants"`
	}
	json.Unmarshal([]byte(o.Engine.ProcessModel("testco")), &model)
	if st := model.Plants[0].Lines[0].Stations; st[len(st)-1].ID != "VI-101" {
		t.Errorf("the running company has no VI-101: %v", st)
	}
	// saved against the config it was made on only
	if _, err := o.ScopeApply(admin, "page", "testco", ScopeRequest{Node: line, Base: v.Base, Config: cfg}); !errors.As(err, &oe) || oe.Code != 409 {
		t.Errorf("stale save: %v", err)
	}
	if vs := o.Engine.Versions("testco"); len(vs) != 1 {
		t.Fatalf("versions: %d", len(vs))
	}
	entries, _ := trail.List(context.Background(), "testco", 0, 100)
	if !hasAction(entries, "company.edit", "产线「曲轴线」：新增工位「在线检测」") {
		t.Errorf("activity: %+v", entries)
	}

	u, err := o.Undo(admin, "page", "testco")
	if err != nil || u.Versions != 0 {
		t.Fatal(u, err)
	}
	if got := o.Engine.CompanyConfig("testco"); got != before {
		t.Errorf("undo did not go back:\n%s\n%s", got, before)
	}
	if _, err := o.Undo(admin, "page", "testco"); !errors.As(err, &oe) || oe.Code != 409 {
		t.Errorf("second undo: %v", err)
	}
	entries, _ = trail.List(context.Background(), "testco", 0, 100)
	if !hasAction(entries, "company.undo", "") {
		t.Error("undo not in the activity log")
	}

	// only administrators of the company
	if _, err := o.Scope(auth.Who{Name: "张工", Role: auth.Operator, Key: "k2"}, "testco", line); err == nil {
		t.Error("an operator opened the editor")
	}
	if _, err := o.Scope(auth.Who{Name: "别家", Role: auth.Admin, Key: "k3", Companies: []string{"other"}}, "testco", line); err == nil {
		t.Error("another company's administrator opened the editor")
	}
}

func hasAction(entries []audit.Entry, action, detail string) bool {
	for _, e := range entries {
		if e.Action == action && e.OK && strings.Contains(e.Detail, detail) {
			return true
		}
	}
	return false
}
