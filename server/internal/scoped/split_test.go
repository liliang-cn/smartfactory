package scoped_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"smartfactory/internal/scoped"
)

const repeated = `{
  "id": "repco", "name": "重复线", "short": "重复", "industry": "forging",
  "plants": [{
    "id": "p1", "name": "一厂", "hall": "锻造厂房",
    "lines": [
      {"name": "连杆线", "product": "conrod", "route": "hot-die-conrod"},
      {"name": "曲轴 {n} 线", "product": "crankshaft", "route": "hot-die-crank", "repeat": 3},
      {"name": "法兰线", "product": "flange", "route": "ring-flange"}
    ]
  }]
}`

func tagsOf(m *scoped.Model) []string {
	var out []string
	for _, l := range m.Plants[0].Lines {
		out = append(out, l.ID+" "+l.Name)
		for _, st := range l.Stations {
			out = append(out, st.ID)
		}
	}
	return out
}

// A copy of a repeated line can be split out and changed alone: names, line numbers and every
// tag stay as they were, the other copies are untouched, and the guard holds the change to it.
func TestSplitCopy(t *testing.T) {
	e := newEngine(t)
	base := model(t, e, []byte(repeated))
	for _, n := range []scoped.Node{
		{Kind: "line", Plant: "p1", Line: "L2", Single: true},
		{Kind: "line", Plant: "p1", Line: "L3", Single: true},
		{Kind: "line", Plant: "p1", Line: "L4", Single: true},
		{Kind: "machine", Plant: "p1", Asset: base.Plants[0].Lines[2].Stations[3].ID, Single: true},
	} {
		cfg, msg, ok := scoped.Split(base, n)
		if !ok || !strings.Contains(msg, "另外 2 条") {
			t.Fatalf("%+v: split %v %q", n, ok, msg)
		}
		raw, _ := json.Marshal(scoped.Compact(base, cfg))
		split := model(t, e, raw)
		if !slices.Equal(tagsOf(base), tagsOf(split)) {
			t.Fatalf("%+v: tags moved:\n%v\n%v", n, tagsOf(base), tagsOf(split))
		}
		s, err := scoped.Resolve(split, n)
		if err != nil || s.Shared != "" {
			t.Fatalf("%+v: resolve %v shared %q", n, err, s.Shared)
		}
		path := s.Path + "/steps/3/cycle"
		if n.Kind == "machine" {
			path = s.Path + "/cycle"
		}
		prop, out, pv := propose(t, e, split, n, set(path, 77.0))
		if len(out) != 0 || len(pv.Tags) != 0 {
			t.Errorf("%+v: out %v tags %v", n, out, pv.Tags)
		}
		changed := 0
		for _, l := range prop.Plants[0].Lines {
			if l.Stations[3].Cycle == 77 {
				changed++
			}
		}
		if changed != 1 {
			t.Errorf("%+v: %d lines changed, want 1", n, changed)
		}
		// another copy is outside the part
		other := "plants/0/lines/0/steps/3/cycle"
		if _, out, _ := propose(t, e, split, n, set(path, 77.0), set(other, 66.0)); len(out) != 1 {
			t.Errorf("%+v: other line not refused: %v", n, out)
		}
	}
	if _, _, ok := scoped.Split(base, scoped.Node{Kind: "line", Plant: "p1", Line: "L1"}); ok {
		t.Error("split a line that is not repeated")
	}
}
