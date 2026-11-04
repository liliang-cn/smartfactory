package plan

import (
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"smartfactory/internal/engine"
)

// A line's rate is measured on a copy of the plant: faults, tool changes and the night's stops
// make it slower than the bottleneck's cycle over the hour's performance a new twin shows.
func TestProbe(t *testing.T) {
	if _, err := os.Stat("../engine/js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng, err := engine.NewOffline(log)
	if err != nil {
		t.Fatal(err)
	}
	s := New(nil, eng, nil, log)
	s.ProbeDays = 1.5
	m := eng.PlanModel("huaduan")
	if m == nil {
		t.Fatal("no model")
	}
	if s.probed("huaduan", m) != nil {
		t.Fatal("a probe cannot be there before it ran")
	}
	if !s.Probing("huaduan") {
		t.Fatal("no probe started")
	}
	s.WaitProbe("huaduan", 5*time.Minute)
	p := s.probed("huaduan", m)
	if p == nil || len(p.Eff) == 0 {
		t.Fatalf("no figures: %+v", p)
	}
	for k, e := range p.Eff {
		t.Logf("%s: %.0f%%", k, e*100)
		if e < 0.2 || e > 1 {
			t.Errorf("%s efficiency %.2f is not a measured one", k, e)
		}
	}
	n := 0
	for _, l := range s.lines("huaduan", m, nil) {
		if l.Neck <= probeMaxCycle {
			n++
			if l.RateSource == "" || l.Eff > 1 {
				t.Errorf("line %s: eff %.2f %s", l.Key, l.Eff, l.RateSource)
			}
		}
	}
	if n == 0 {
		t.Fatal("no short-cycle lines")
	}
}
