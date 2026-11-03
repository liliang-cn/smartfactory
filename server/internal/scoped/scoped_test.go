package scoped_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"testing"

	"smartfactory/internal/engine"
	"smartfactory/internal/scoped"
)

// A forging company of one plant, two areas: 曲轴线 and 连杆线 in 锻造车间 (FG), 法兰线 in 精整车间 (FN).
const company = `{
  "id": "testco", "name": "测试锻造", "short": "测试", "industry": "forging",
  "plants": [{
    "id": "p1", "name": "一厂", "hall": "锻造厂房",
    "areas": [{"name": "锻造车间", "code": "FG"}, {"name": "精整车间", "code": "FN"}],
    "lines": [
      {"name": "曲轴线", "product": "crankshaft", "route": "hot-die-crank", "area": "锻造车间"},
      {"name": "连杆线", "product": "conrod", "route": "hot-die-conrod", "area": "锻造车间"},
      {"name": "法兰线", "product": "flange", "route": "ring-flange", "area": "精整车间"}
    ]
  }]
}`

func newEngine(t *testing.T) *engine.Engine {
	t.Helper()
	if _, err := os.Stat("../engine/js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	e, err := engine.NewOffline(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func model(t *testing.T, e *engine.Engine, cfg []byte) *scoped.Model {
	t.Helper()
	r := e.ScopeModel(cfg)
	if !r.OK {
		t.Fatalf("scope model: %v", r.Errors)
	}
	m, err := scoped.Parse(r.Detail)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// propose is what the server does with a change: apply it, keep it compact, lay it out again,
// guard it, say what it does.
func propose(t *testing.T, e *engine.Engine, base *scoped.Model, n scoped.Node, changes ...scoped.Change) (*scoped.Model, []string, scoped.Preview) {
	t.Helper()
	s, err := scoped.Resolve(base, n)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := scoped.Apply(base.Config, changes)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(scoped.Compact(base, cfg))
	prop := model(t, e, raw)
	return prop, s.Outside(base, prop), scoped.Describe(base, prop)
}

func set(path string, v any) scoped.Change { return scoped.Change{Op: "set", Path: path, Value: v} }
func insert(path string, v any) scoped.Change {
	return scoped.Change{Op: "insert", Path: path, Value: v}
}

func has(t *testing.T, list []string, want string) {
	t.Helper()
	for _, s := range list {
		if strings.Contains(s, want) {
			return
		}
	}
	t.Errorf("no %q in %q", want, list)
}

// Tags follow line order and the count of each kind on the line: the editor sees them as the
// twin does, and knows which config entry and step each is.
func TestModelTags(t *testing.T) {
	e := newEngine(t)
	m := model(t, e, []byte(company))
	p := m.Plants[0]
	if len(p.Lines) != 3 || p.Lines[1].ID != "L2" || p.Lines[1].Cfg != 1 || p.Lines[2].Area != "FN" {
		t.Fatalf("lines: %+v", p.Lines)
	}
	if got := p.Lines[0].Stations[3]; got.ID != "HP-101" || got.Key != "die-press" {
		t.Errorf("L1 step 3: %+v", got)
	}
	if got := p.Lines[1].Stations[0].ID; got != "SAW-201" {
		t.Errorf("L2 first tag %s", got)
	}
	// each line's routing is written out as its steps, marked with where they came from
	l := m.Config["plants"].([]any)[0].(map[string]any)["lines"].([]any)[0].(map[string]any)
	if l["$route"] != "hot-die-crank" || len(l["steps"].([]any)) != 9 || l["route"] != nil {
		t.Errorf("line 0 not written out: %v", l)
	}
}

// The guard: a change anywhere outside the part picked is caught and named, inside it passes.
func TestScopeGuard(t *testing.T) {
	e := newEngine(t)
	base := model(t, e, []byte(company))
	line := scoped.Node{Kind: "line", Plant: "p1", Line: "L1"}

	_, out, _ := propose(t, e, base, line, set("plants/0/lines/0/steps/3/cycle", 40.0))
	if len(out) != 0 {
		t.Errorf("own station refused: %v", out)
	}
	_, out, _ = propose(t, e, base, line, set("plants/0/lines/0/steps/3/cycle", 40.0), set("plants/0/lines/1/steps/3/cycle", 40.0))
	if len(out) != 1 || !strings.Contains(out[0], "产线「连杆线」工位「") || !strings.Contains(out[0], "的节拍") {
		t.Errorf("another line's cycle: %v", out)
	}
	_, out, _ = propose(t, e, base, line, set("name", "别的公司"))
	if len(out) != 1 || out[0] != "公司的名称" {
		t.Errorf("company name: %v", out)
	}
	_, out, _ = propose(t, e, base, line, set("plants/0/id", "p9"))
	has(t, out, "工厂「一厂」的编号")
	// a line cannot take itself away: the lines after it would be renumbered
	_, out, _ = propose(t, e, base, scoped.Node{Kind: "line", Plant: "p1", Line: "L3"}, scoped.Change{Op: "remove", Path: "plants/0/lines/2"})
	has(t, out, "法兰线")

	// a machine: only its own step
	hp := scoped.Node{Kind: "machine", Plant: "p1", Asset: "HP-101"}
	_, out, _ = propose(t, e, base, hp, set("plants/0/lines/0/steps/3/thermal", map[string]any{"warn": 80.0, "trip": 92.0}))
	if len(out) != 0 {
		t.Errorf("machine's own limits refused: %v", out)
	}
	_, out, _ = propose(t, e, base, hp, set("plants/0/lines/0/steps/4/cycle", 30.0))
	has(t, out, "产线「曲轴线」工位「")
	_, out, _ = propose(t, e, base, hp, insert("plants/0/lines/0/steps/-", map[string]any{"machine": "vision"}))
	if len(out) == 0 {
		t.Error("a machine added a station to its line")
	}

	// an area: its own fields, its lines, and lines moved into it; not the lines of another
	fg := scoped.Node{Kind: "area", Plant: "p1", Area: "FG"}
	_, out, _ = propose(t, e, base, fg, set("plants/0/areas/0/name", "热锻车间"),
		set("plants/0/lines/0/area", "热锻车间"), set("plants/0/lines/1/area", "热锻车间"), set("plants/0/lines/1/steps/0/cycle", 50.0))
	if len(out) != 0 {
		t.Errorf("area rename refused: %v", out)
	}
	_, out, _ = propose(t, e, base, fg, set("plants/0/lines/2/area", "锻造车间"))
	if len(out) != 0 {
		t.Errorf("moving 法兰线 in refused: %v", out)
	}
	_, out, _ = propose(t, e, base, fg, set("plants/0/lines/2/steps/0/cycle", 50.0))
	has(t, out, "产线「法兰线」")
	_, out, _ = propose(t, e, base, fg, set("plants/0/areas/1/note", "外协"))
	has(t, out, "车间「精整车间」的备注")

	// the plant: everything of it but its id
	_, out, _ = propose(t, e, base, scoped.Node{Kind: "plant", Plant: "p1"}, set("plants/0/name", "常州厂"), set("plants/0/lines/2/steps/0/cycle", 50.0))
	if len(out) != 0 {
		t.Errorf("plant change refused: %v", out)
	}
}

// Each kind of part takes its patch, and the preview says it in words.
func TestPatchEachKind(t *testing.T) {
	e := newEngine(t)
	base := model(t, e, []byte(company))

	_, out, pv := propose(t, e, base, scoped.Node{Kind: "plant", Plant: "p1"}, set("plants/0/name", "常州厂"), set("plants/0/badge", "常州"))
	if len(out) != 0 || !slices.Contains(pv.Changes, "工厂「一厂」：名称 一厂 → 常州厂") || !slices.Contains(pv.Changes, "工厂「一厂」：徽标 （空） → 常州") {
		t.Errorf("plant: %v %v", out, pv.Changes)
	}

	_, out, pv = propose(t, e, base, scoped.Node{Kind: "area", Plant: "p1", Area: "FG"},
		set("plants/0/areas/0/name", "热锻车间"), set("plants/0/areas/0/note", "两班"),
		set("plants/0/lines/0/area", "热锻车间"), set("plants/0/lines/1/area", "热锻车间"))
	if len(out) != 0 || !slices.Contains(pv.Changes, "车间「锻造车间」：名称 锻造车间 → 热锻车间") || !slices.Contains(pv.Changes, "车间「锻造车间」：备注 （空） → 两班") {
		t.Errorf("area: %v %v", out, pv.Changes)
	}
	// the lines follow their area's new name: not a move
	for _, c := range pv.Changes {
		if strings.Contains(c, "所在车间") {
			t.Errorf("area rename reads as a move: %s", c)
		}
	}

	prop, out, pv := propose(t, e, base, scoped.Node{Kind: "line", Plant: "p1", Line: "L1"},
		set("plants/0/lines/0/name", "曲轴锻造线"), set("plants/0/lines/0/speed", 0.7), set("plants/0/lines/0/steps/3/cycle", 40.0))
	hp := base.Plants[0].Lines[0].Stations[3]
	if len(out) != 0 || !slices.Contains(pv.Changes, "产线「曲轴线」：名称 曲轴线 → 曲轴锻造线") || !slices.Contains(pv.Changes, "产线「曲轴线」：传送带速度 （空） → 0.7 m/s") ||
		!slices.Contains(pv.Changes, "产线「曲轴线」工位「"+hp.Name+"」：节拍 "+fmtNum(hp.Cycle)+" s → 40 s") {
		t.Errorf("line: %v %v", out, pv.Changes)
	}
	if prop.Plants[0].Lines[0].Stations[3].Cycle != 40 || prop.Plants[0].Lines[1].Stations[3].Cycle != base.Plants[0].Lines[1].Stations[3].Cycle {
		t.Error("the cycle is not where it was set")
	}

	prop, out, pv = propose(t, e, base, scoped.Node{Kind: "machine", Plant: "p1", Asset: "HP-101"},
		set("plants/0/lines/0/steps/3/thermal/warn", 80.0), set("plants/0/lines/0/steps/3/thermal/trip", 92.0), set("plants/0/lines/0/steps/3/name", "主锻压"))
	if len(out) != 0 || !slices.Contains(pv.Changes, "产线「曲轴线」工位「"+hp.Name+"」：报警温度 72°C → 80°C") ||
		!slices.Contains(pv.Changes, "产线「曲轴线」工位「"+hp.Name+"」：停机温度 84°C → 92°C") || len(pv.Tags) != 0 {
		t.Errorf("machine: %v %v %v", out, pv.Changes, pv.Tags)
	}
	if st := prop.Plants[0].Lines[0].Stations[3]; st.Warn != 80 || st.Trip != 92 || st.Name != "主锻压" {
		t.Errorf("machine after: %+v", st)
	}
	// the other die press (连杆线) keeps its pack's limits
	if st := prop.Plants[0].Lines[1].Stations[3]; st.Warn != 72 {
		t.Errorf("连杆线 press: %+v", st)
	}
	// an alarm above the trip is refused by the layout
	cfg, _ := scoped.Apply(base.Config, []scoped.Change{set("plants/0/lines/0/steps/3/thermal/warn", 90.0)})
	raw, _ := json.Marshal(scoped.Compact(base, cfg))
	if r := e.ScopeModel(raw); r.OK || !strings.Contains(strings.Join(r.Errors, ""), "停机温度") {
		t.Errorf("warn above trip: %v", r.Errors)
	}
}

// Kept compact: a line whose steps are its routing's names the routing again; one changed keeps
// its own steps.
func TestCompact(t *testing.T) {
	e := newEngine(t)
	base := model(t, e, []byte(company))
	cfg, _ := scoped.Apply(base.Config, []scoped.Change{set("plants/0/lines/0/steps/3/cycle", 40.0)})
	out := scoped.Compact(base, cfg)
	lines := out["plants"].([]any)[0].(map[string]any)["lines"].([]any)
	l0, l1 := lines[0].(map[string]any), lines[1].(map[string]any)
	if l0["route"] != nil || len(l0["steps"].([]any)) != 9 || l0["$route"] != nil {
		t.Errorf("changed line: %v", l0)
	}
	if l1["route"] != "hot-die-conrod" || l1["steps"] != nil || l1["$route"] != nil {
		t.Errorf("unchanged line: %v", l1)
	}
}

// Tags: a station added at the end moves no tag; added in the middle, only the later stations of
// the same kind move up one, and the preview says which.
func TestTagStability(t *testing.T) {
	e := newEngine(t)
	base := model(t, e, []byte(company))
	line := scoped.Node{Kind: "line", Plant: "p1", Line: "L1"}

	_, _, pv := propose(t, e, base, line, insert("plants/0/lines/0/steps/-", map[string]any{"machine": "vision", "name": "在线检测"}))
	if len(pv.Tags) != 0 || !slices.Equal(pv.Added, []string{"VI-101"}) {
		t.Errorf("append: tags %v added %v", pv.Tags, pv.Added)
	}
	has(t, pv.Changes, "产线「曲轴线」：新增工位「在线检测」，排在「")
	has(t, pv.Changes, "（VI-101）")

	// a kind the line has nowhere else, in the middle: still nothing moves
	_, _, pv = propose(t, e, base, line, insert("plants/0/lines/0/steps/2", map[string]any{"machine": "vision", "name": "坯料检测"}))
	if len(pv.Tags) != 0 {
		t.Errorf("new kind in the middle moved tags: %v", pv.Tags)
	}

	// another die press before the one there: that one becomes HP-102
	_, _, pv = propose(t, e, base, line, insert("plants/0/lines/0/steps/3", map[string]any{"machine": "die-press", "name": "预锻"}))
	if len(pv.Tags) != 1 || pv.Tags[0].From != "HP-101" || pv.Tags[0].To != "HP-102" || !slices.Equal(pv.Added, []string{"HP-101"}) {
		t.Errorf("press in front: tags %+v added %v", pv.Tags, pv.Added)
	}
	// … after it: the new one is HP-102 and nothing moves
	_, _, pv = propose(t, e, base, line, insert("plants/0/lines/0/steps/4", map[string]any{"machine": "die-press", "name": "终锻"}))
	if len(pv.Tags) != 0 || !slices.Equal(pv.Added, []string{"HP-102"}) {
		t.Errorf("press behind: tags %+v added %v", pv.Tags, pv.Added)
	}
	// a station taken out: its tag goes
	_, _, pv = propose(t, e, base, line, scoped.Change{Op: "remove", Path: "plants/0/lines/0/steps/7"})
	if len(pv.Tags) != 1 || pv.Tags[0].From != "MT-101" || pv.Tags[0].To != "" {
		t.Errorf("removed: %+v", pv.Tags)
	}
	has(t, pv.Changes, "去掉工位")
	// a station of another kind in its place: its kind (and tag) change
	_, _, pv = propose(t, e, base, line, set("plants/0/lines/0/steps/7", map[string]any{"machine": "ut", "name": "超声探伤"}))
	if len(pv.Tags) != 1 || pv.Tags[0].From != "MT-101" || pv.Tags[0].To != "UT-101" {
		t.Errorf("kind swapped: %+v", pv.Tags)
	}
	has(t, pv.Changes, "设备种类 荧光磁粉探伤 → 超声波探伤")
}

// Lines keep their numbers: a repeat added to the first line renumbers every machine after it,
// and the preview lists each one.
func TestLineNumbersShift(t *testing.T) {
	e := newEngine(t)
	base := model(t, e, []byte(company))
	_, out, pv := propose(t, e, base, scoped.Node{Kind: "line", Plant: "p1", Line: "L1"}, set("plants/0/lines/0/repeat", 2.0))
	if len(out) != 0 {
		t.Fatal(out)
	}
	moved := map[string]string{}
	for _, tc := range pv.Tags {
		moved[tc.From] = tc.To
	}
	_, first := moved["HP-101"]
	if moved["SAW-201"] != "SAW-301" || moved["SAW-301"] != "SAW-401" || first {
		t.Errorf("renumbered: %v", moved)
	}
	has(t, pv.Changes, "产线「曲轴线」：条数 1 → 2")
}

func fmtNum(f float64) string {
	b, _ := json.Marshal(f)
	return string(b)
}
