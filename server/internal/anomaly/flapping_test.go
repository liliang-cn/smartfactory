package anomaly

import (
	"math"
	"math/rand/v2"
	"testing"
	"time"

	"smartfactory/internal/tags"
)

// A hint that ends does not start again at the next excursion like it: it waits Cooldown,
// unless the reading is much worse than it was.
func TestCooldown(t *testing.T) {
	r := newRig(t, nil)
	r.state("running")
	r.run("vib", time.Hour, 0.2, flat(4))
	excursion := func(v float64) { r.run("vib", 2*time.Minute, 0.2, flat(v)); r.run("vib", 7*time.Minute, 0.2, flat(4)) }
	excursion(5)
	excursion(5)
	excursion(5)
	if n := len(r.d.events); n != 2 {
		t.Fatalf("three excursions alike within the cooldown: %d events, want one hint begun and ended", n)
	}
	// much worse: a hint at once
	r.run("vib", 2*time.Minute, 0.2, flat(6.5))
	if h := r.hints(); len(h) != 1 || h[0].Peak < 9 {
		t.Fatalf("a much worse excursion: %+v", h)
	}
	r.run("vib", 7*time.Minute, 0.2, flat(4))
	if len(r.hints()) != 0 {
		t.Fatal("the much worse hint did not end")
	}
	// past the cooldown, the same again is a hint
	r.run("vib", 31*time.Minute, 0.2, flat(4))
	r.run("vib", 2*time.Minute, 0.2, flat(5))
	if len(r.hints()) != 1 {
		t.Fatal("no hint once the cooldown was over")
	}
}

// Back within for less than Clear does not end a hint (and so does not start another).
func TestClearHysteresis(t *testing.T) {
	r := newRig(t, nil)
	r.state("running")
	r.run("vib", time.Hour, 0.2, flat(4))
	r.run("vib", 2*time.Minute, 0.2, flat(5))
	for range 5 {
		r.run("vib", 3*time.Minute, 0.2, flat(4))
		r.run("vib", 2*time.Minute, 0.2, flat(5))
	}
	if n := len(r.d.events); n != 1 {
		t.Fatalf("an excursion coming and going every few minutes: %d events, want the one hint", n)
	}
}

// A machine silent for a while (the server or the equipment restarted) has moved on: a press that
// cooled is left to warm up, then judged as before; one that stays out past SettleMax is a hint.
func TestGapSettles(t *testing.T) {
	r := newRig(t, nil)
	r.state("running")
	r.run("temp", time.Hour, 0.5, flat(60))
	r.now = r.now.Add(20 * time.Minute)
	// cooled to 40, warming back over half an hour
	r.run("temp", 30*time.Minute, 0.5, func(i int) float64 { return 40 + 20*float64(i)/1800 })
	r.run("temp", 10*time.Minute, 0.5, flat(60))
	if h := r.hints(); len(h) != 0 {
		t.Fatalf("warming up after a gap: %+v", h)
	}
	r.run("temp", 2*time.Minute, 0.5, flat(65))
	if len(r.hints()) != 1 {
		t.Fatal("not judged once it had settled")
	}
	r.run("temp", 10*time.Minute, 0.5, flat(60))
	r2 := newRig(t, nil)
	r2.state("running")
	r2.run("temp", time.Hour, 0.5, flat(60))
	r2.now = r2.now.Add(20 * time.Minute)
	r2.run("temp", 2*time.Hour+5*time.Minute, 0.5, flat(50))
	if len(r2.hints()) != 1 {
		t.Fatal("never back after a gap, and no hint past SettleMax")
	}
}

// A slow field (a temperature that takes most of an hour to follow its machine) dipping for half
// an hour after each pause is how it works, not a hint; staying off for hours is.
func TestSlowFieldTransients(t *testing.T) {
	r := newRig(t, nil)
	r.d.Set.Warmup = 24 * time.Hour
	r.state("running")
	temp := 60.0
	step := func(d time.Duration, target float64) {
		for range int(d / time.Second) {
			temp += (target - temp) / 2400
			r.read("temp", temp+r.rnd.NormFloat64()*0.2)
			r.now = r.now.Add(time.Second)
		}
	}
	// two days of running with a pause (still "running": waiting for parts) every hour or so
	for i := range 48 {
		step(time.Duration(45+i%4*5)*time.Minute, 60)
		step(time.Duration(3+i%3)*time.Minute, 30)
	}
	if h := r.hints(); len(h) != 0 {
		t.Fatalf("a slow temperature dipping after pauses: %s", h[0].Text)
	}
	m, _ := r.d.Machine("c", "p", "M1")
	t.Logf("normal %.2f±%.2f, pace %s", m.Fields[0].Now.Mean, m.Fields[0].Now.Sigma, r.d.machines[mkey{"c", "p", "M1"}].fields["temp"].cur.pace())
	// a cooling that stopped working: hours higher
	step(3*time.Hour, 70)
	if len(r.hints()) != 1 {
		t.Fatal("a temperature hours off went unnoticed")
	}
}

// A signal that wanders by nature, within bounds (a bounded random walk), stays mostly within
// DriftK and does not stay beyond it for half an hour: no drift.
func TestWanderingNoDrift(t *testing.T) {
	for seed := uint64(1); seed <= 10; seed++ {
		r := newRig(t, nil)
		r.rnd = rand.New(rand.NewPCG(seed, 3))
		r.d.Set.Warmup = 24 * time.Hour
		r.state("running")
		// an AR(1) walk with a correlation time of about two minutes, plus reading noise
		w := 0.0
		r.run("vib", 72*time.Hour, 0, func(int) float64 {
			w = 0.992*w + r.rnd.NormFloat64()*0.05
			return 4 + w + r.rnd.NormFloat64()*0.1
		})
		// its rare far excursions (4σ, a few minutes) are level hints, now and then; never a drift
		levels := 0
		for len(r.d.events) > 0 {
			e := <-r.d.events
			switch {
			case e.ended:
			case e.a.Kind == Drift:
				t.Errorf("seed %d: %s", seed, e.a.Text)
			default:
				levels++
			}
		}
		if levels > 2 {
			t.Errorf("seed %d: %d level hints in two days", seed, levels)
		}
	}
}

// Inventory, capacity, set-points and measured parts are not a machine's condition.
func TestSkippedFields(t *testing.T) {
	r := newRig(t, nil)
	r.state("running")
	r.now = r.now.Add(10 * time.Second)
	for _, f := range []string{"inv.punch", "cap.punch", "lastMeasure", "measure", "speed", "task"} {
		r.read(f, 3)
	}
	r.d.Set.Skip = nil
	d, _ := New(nil, Settings{Skip: []string{"gauge*"}}, nil, quiet)
	if !d.skipped("gauge_temp") || !d.skipped("inv.roll") || d.skipped("temp") {
		t.Error("skip prefixes")
	}
	if m, ok := r.d.Machine("c", "p", "M1"); ok && len(m.Fields) > 0 {
		t.Fatalf("learned %+v", m.Fields)
	}
}

// Past MaxOpen open hints a company's new ones wait, counted, until there are fewer; the report
// says so.
func TestValve(t *testing.T) {
	r := newRig(t, nil)
	r.d.Set.MaxOpen, r.d.Set.MaxPerHour = 4, 100
	add := func(asset string, v float64) {
		r.d.Add(tags.Tag{Company: "c", Plant: "p", Asset: asset, Field: "vib", Value: v + r.rnd.NormFloat64()*0.2, Source: "plc", At: r.now})
	}
	for i := range 6 {
		r.d.Add(tags.Tag{Company: "c", Plant: "p", Asset: string(rune('A' + i)), Field: "state", Value: "running", Source: "plc", At: r.now})
	}
	tick := func(d time.Duration, v func(i int) float64) {
		for range int(d / time.Second) {
			for i := range 6 {
				add(string(rune('A'+i)), v(i))
			}
			r.now = r.now.Add(time.Second)
		}
	}
	tick(time.Hour, func(int) float64 { return 4 })
	tick(2*time.Minute, func(int) float64 { return 6 })
	rep, _ := r.d.Report("c")
	if len(rep.Current) != 4 || !rep.Valve.Paused || rep.Valve.HeldBack != 2 || rep.Valve.Open != 4 {
		t.Fatalf("all six out: %d hints, valve %+v", len(rep.Current), rep.Valve)
	}
	// back to normal: the hints end and the valve opens
	tick(7*time.Minute, func(int) float64 { return 4 })
	tick(40*time.Minute, func(int) float64 { return 4 })
	tick(2*time.Minute, func(i int) float64 { return map[bool]float64{true: 6, false: 4}[i == 0] })
	rep, _ = r.d.Report("c")
	if len(rep.Current) != 1 || rep.Valve.Paused {
		t.Fatalf("after: %d hints, valve %+v", len(rep.Current), rep.Valve)
	}
	if math.IsNaN(float64(rep.Valve.LastHour)) {
		t.Fatal()
	}
}
