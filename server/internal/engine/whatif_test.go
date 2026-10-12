package engine

import (
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"os"
	"testing"
)

// A what-if over several seeds gives each line's mean and, beside it, P10 and P90 over the runs;
// the change's band is of the paired differences, seed by seed.
func TestWhatIfBand(t *testing.T) {
	if _, err := os.Stat("js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	e, err := NewOffline(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	// lines and changes not given (nil from Go, null in JSON) are all lines and no change
	var none struct {
		OK bool `json:"ok"`
	}
	if raw := e.WhatIf("huaduan", "p1", nil, 0.05, 2, nil, 7, 1); json.Unmarshal([]byte(raw), &none) != nil || !none.OK {
		t.Fatalf("no lines, no changes: %.300s", raw)
	}
	changes := []map[string]any{{"match": "HP", "command": "speed", "arg": 1.3}}
	raw := e.WhatIf("huaduan", "p1", []string{"曲轴 1 线", "曲轴 2 线"}, 2, 2, changes, 7, 3)
	type line map[string]any
	var out struct {
		OK   bool `json:"ok"`
		Base struct {
			Lines []line `json:"lines"`
		} `json:"现状"`
		Plan struct {
			Lines []line `json:"lines"`
		} `json:"方案"`
		Change []line `json:"变化"`
		Runs   []struct {
			Base []line `json:"现状"`
			Plan []line `json:"方案"`
		} `json:"每次"`
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil || !out.OK {
		t.Fatalf("%v: %.500s", err, raw)
	}
	num := func(v any) float64 { f, _ := v.(float64); return f }
	for _, side := range []struct {
		name  string
		lines []line
	}{{"现状", out.Base.Lines}, {"方案", out.Plan.Lines}, {"变化", out.Change}} {
		if len(side.lines) == 0 {
			t.Fatalf("%s: no lines", side.name)
		}
		for _, l := range side.lines {
			p10, ok10 := l["p10"].(map[string]any)
			p90, ok90 := l["p90"].(map[string]any)
			if !ok10 || !ok90 {
				t.Fatalf("%s %v: no band: %v", side.name, l["line"], l)
			}
			for _, k := range []string{"produced", "oee", "scrapRate", "availability"} {
				if l[k] == nil {
					continue
				}
				lo, mean, hi := num(p10[k]), num(l[k]), num(p90[k])
				if lo > mean+1e-3 || mean > hi+1e-3 {
					t.Errorf("%s %v %s: p10 %g, mean %g, p90 %g", side.name, l["line"], k, lo, mean, hi)
				}
			}
		}
	}
	// the change's band is the spread of each seed's own difference, not of the two sides' bands
	for i, l := range out.Change {
		var diffs []float64
		for _, r := range out.Runs {
			diffs = append(diffs, num(r.Plan[i]["oee"])-num(r.Base[i]["oee"]))
		}
		lo, hi := math.Inf(1), math.Inf(-1)
		for _, d := range diffs {
			lo, hi = math.Min(lo, d), math.Max(hi, d)
		}
		p10, p90 := num(l["p10"].(map[string]any)["oee"]), num(l["p90"].(map[string]any)["oee"])
		if p10 < lo-1e-3 || p90 > hi+1e-3 || p10 > p90 {
			t.Errorf("%v oee change band %g…%g outside the runs' %g…%g", l["line"], p10, p90, lo, hi)
		}
		t.Logf("%v: OEE %+.3f (P10 %+.3f, P90 %+.3f)", l["line"], num(l["oee"]), p10, p90)
	}
}
