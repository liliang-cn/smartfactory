package anomaly

import (
	"io"
	"log/slog"
	"math"
	"math/rand/v2"
	"testing"
	"time"

	"smartfactory/internal/audit"
	"smartfactory/internal/config"
	"smartfactory/internal/tags"
)

func configDefaults() config.Anomaly { return config.Anomaly{} }

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// rig feeds a detector readings once a second on a clock of its own.
type rig struct {
	t   *testing.T
	d   *Detector
	now time.Time
	rnd *rand.Rand
}

func newRig(t *testing.T, st *Store) *rig {
	set := SettingsFrom(configDefaults())
	set.Warmup = 30 * time.Minute
	d, err := New(st, set, nil, quiet)
	if err != nil {
		t.Fatal(err)
	}
	r := &rig{t: t, d: d, now: time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC), rnd: rand.New(rand.NewPCG(1, 2))}
	d.Now = func() time.Time { return r.now }
	return r
}

func (r *rig) state(s string) {
	r.d.Add(tags.Tag{Company: "c", Plant: "p", Asset: "M1", Field: "state", Value: s, Source: "plc", At: r.now})
}

func (r *rig) read(field string, v float64) {
	r.d.Add(tags.Tag{Company: "c", Plant: "p", Asset: "M1", Field: field, Value: v, Source: "plc", At: r.now})
}

// run reads field for d, a second apart, from v(i) plus gaussian noise of sd.
func (r *rig) run(field string, d time.Duration, sd float64, v func(i int) float64) {
	for i := 0; i < int(d/time.Second); i++ {
		r.read(field, v(i)+r.rnd.NormFloat64()*sd)
		r.now = r.now.Add(time.Second)
	}
}

func (r *rig) hints() []Anomaly { return r.d.Current("c") }

func flat(x float64) func(int) float64 { return func(int) float64 { return x } }

// Hours of noise, white and slowly wandering, raise nothing.
func TestNoiseRaisesNothing(t *testing.T) {
	r := newRig(t, nil)
	r.state("running")
	r.run("temp", 6*time.Hour, 1, flat(50))
	// vibration that wanders: an AR(1) series (each reading 0.95 of the last deviation)
	dev := 0.0
	r.run("vib", 6*time.Hour, 0, func(int) float64 {
		dev = 0.95*dev + r.rnd.NormFloat64()*0.1
		return 4 + dev
	})
	if h := r.hints(); len(h) != 0 {
		t.Fatalf("noise raised %d hints: %v", len(h), h[0].Text)
	}
}

// A step of 5 σ that lasts is a hint after Sustain, not before; back to normal, it ends.
func TestStepChange(t *testing.T) {
	r := newRig(t, nil)
	r.state("running")
	r.run("vib", time.Hour, 0.2, flat(4))
	start := r.now
	var raised time.Time
	for i := 0; i < 180; i++ {
		r.read("vib", 5+r.rnd.NormFloat64()*0.2)
		if raised.IsZero() && len(r.hints()) > 0 {
			raised = r.now
		}
		r.now = r.now.Add(time.Second)
	}
	if raised.IsZero() {
		t.Fatal("no hint for a 5σ step lasting 3 minutes")
	}
	if wait := raised.Sub(start); wait < time.Minute || wait > 70*time.Second {
		t.Errorf("hint after %s; want about a minute", wait)
	}
	h := r.hints()[0]
	if h.Kind != Level || h.Field != "vib" || h.State != "running" || h.Z < 3.5 || math.Abs(h.Mean-4) > 0.05 || math.Abs(h.Sigma-0.2) > 0.03 {
		t.Errorf("hint %+v", h)
	}
	if h.Text == "" {
		t.Error("no text")
	}
	// the step is not learned: the normal is still 4
	m, _ := r.d.Machine("c", "p", "M1")
	if m.Fields[0].Now == nil || math.Abs(m.Fields[0].Now.Mean-4) > 0.05 {
		t.Errorf("normal moved with the step: %+v", m.Fields[0].Now)
	}
	r.run("vib", 4*time.Minute, 0.2, flat(4))
	if len(r.hints()) != 1 {
		t.Fatal("the hint ended before the readings were back for Clear")
	}
	r.run("vib", 2*time.Minute, 0.2, flat(4))
	if len(r.hints()) != 0 {
		t.Fatal("the hint did not end once the readings were back")
	}
	if e := <-r.d.events; e.ended || e.a.Kind != Level {
		t.Errorf("first event %+v", e)
	}
	if e := <-r.d.events; !e.ended || e.a.Ended == nil {
		t.Errorf("second event %+v", e)
	}
}

// One wild reading, or a few seconds of them, is not a hint.
func TestSpikeIsNotAHint(t *testing.T) {
	r := newRig(t, nil)
	r.state("running")
	r.run("temp", time.Hour, 1, flat(50))
	r.run("temp", 30*time.Second, 1, flat(70))
	r.run("temp", time.Minute, 1, flat(50))
	if len(r.hints()) != 0 {
		t.Fatal("a 30 s excursion raised a hint")
	}
}

// A slow drift is noticed by its average, held DriftK σ off for DriftSustain, before readings
// stay K σ out.
func TestSlowDrift(t *testing.T) {
	r := newRig(t, nil)
	r.state("running")
	r.run("vib", time.Hour, 0.2, flat(4))
	// +0.2 (1 σ) an hour
	t0 := r.now
	for i := 0; i < 6*3600 && len(r.hints()) == 0; i++ {
		r.read("vib", 4+0.2*float64(i)/3600+r.rnd.NormFloat64()*0.2)
		r.now = r.now.Add(time.Second)
	}
	h := r.hints()
	if len(h) == 0 {
		t.Fatal("a drift of 6 σ over 6 hours went unnoticed")
	}
	drifted := r.now.Sub(t0).Hours() * 0.2
	if h[0].Kind != Drift || math.Abs(h[0].Z) < 2 || math.Abs(h[0].Z) >= 3 || r.now.Sub(t0) < 2*time.Hour {
		t.Errorf("hint %s %+.2fσ after %.2f of drift (%s): want a drift hint at 2–3σ, after half an hour past 2σ", h[0].Kind, h[0].Z, drifted, r.now.Sub(t0))
	}
	t.Logf("noticed after %s, %.2f mm/s off: %s", r.now.Sub(t0).Round(time.Minute), drifted, h[0].Text)
}

// Running and idle have their own normals; switching between them is not unusual, a running
// value while idle is.
func TestStateSwitch(t *testing.T) {
	r := newRig(t, nil)
	for i := 0; i < 24; i++ {
		r.state("running")
		r.run("power", 5*time.Minute, 2, flat(120))
		r.state("idle")
		r.run("power", 5*time.Minute, 0.5, flat(15))
	}
	if h := r.hints(); len(h) != 0 {
		t.Fatalf("switching states raised %s", h[0].Text)
	}
	m, _ := r.d.Machine("c", "p", "M1")
	if len(m.Fields) != 1 || len(m.Fields[0].States) != 2 {
		t.Fatalf("machine %+v", m)
	}
	for _, b := range m.Fields[0].States {
		want := map[string]float64{"running": 120, "idle": 15}[b.State]
		if !b.Warm || math.Abs(b.Mean-want) > 1 {
			t.Errorf("%s normal %+v, want %g", b.State, b, want)
		}
	}
	// idle, but drawing running power
	r.run("power", 2*time.Minute, 2, flat(120))
	h := r.hints()
	if len(h) != 1 || h[0].State != "idle" || h[0].Z < 10 {
		t.Fatalf("hints %+v", h)
	}
	// running again, as usual: that says nothing of the idle hint, which keeps its numbers
	r.state("running")
	r.run("power", 2*time.Minute, 2, flat(120))
	if h := r.hints(); len(h) != 1 || h[0].Z < 10 {
		t.Errorf("the idle hint, held against running readings: %+v", h)
	}
	if m, _ := r.d.Machine("c", "p", "M1"); m.Fields[0].Now.State != "running" || math.Abs(m.Fields[0].Z) > 4 {
		t.Errorf("power now %+v, %.1fσ", m.Fields[0].Now, m.Fields[0].Z)
	}
}

// Equipment sends a machine's fields in name order, power before state: the power read with a
// change of state belongs to the new state, not the old one's normal.
func TestStateChangeInOneMessage(t *testing.T) {
	r := newRig(t, nil)
	for i := 0; i < 2*3600; i++ {
		state, power := "running", 120.0
		if (i/300)%2 == 1 {
			state, power = "idle", 15.0
		}
		r.read("power", power+r.rnd.NormFloat64()*0.5)
		// stamped as the hub publishes it, a moment after the power
		r.d.Add(tags.Tag{Company: "c", Plant: "p", Asset: "M1", Field: "state", Value: state, At: r.now.Add(40 * time.Microsecond)})
		r.now = r.now.Add(time.Second)
	}
	m, _ := r.d.Machine("c", "p", "M1")
	for _, b := range m.Fields[0].States {
		// one reading of the other state in each ten minutes would make it several times wider
		if b.Sigma > 2 {
			t.Errorf("%s normal %.2f±%.2f (%d): readings of the other state learned into it", b.State, b.Mean, b.Sigma, b.N)
		}
	}
	if h := r.hints(); len(h) != 0 {
		t.Errorf("hints %+v", h)
	}
}

// A running hint is not moved by the power read in the report that says the machine went idle.
func TestHintKeepsToItsState(t *testing.T) {
	r := newRig(t, nil)
	r.state("running")
	r.run("power", time.Hour, 0.5, flat(26))
	r.run("power", 2*time.Minute, 0.5, flat(30))
	h := r.hints()
	if len(h) != 1 || h[0].Peak < 5 {
		t.Fatalf("hints %+v", h)
	}
	peak := h[0].Peak
	r.read("power", 4)
	r.d.Add(tags.Tag{Company: "c", Plant: "p", Asset: "M1", Field: "state", Value: "idle", At: r.now.Add(30 * time.Microsecond)})
	if h := r.hints(); len(h) != 1 || h[0].Peak != peak || h[0].Z < 5 {
		t.Errorf("hint after going idle: %+v, peak was %.1f", h, peak)
	}
}

// Nothing is judged while a field is still being learned.
func TestWarmup(t *testing.T) {
	r := newRig(t, nil)
	r.state("running")
	r.run("temp", 20*time.Minute, 1, flat(50))
	r.run("temp", 3*time.Minute, 1, flat(80))
	if len(r.hints()) != 0 {
		t.Fatal("judged before warming up")
	}
	m, _ := r.d.Machine("c", "p", "M1")
	if b := m.Fields[0].Now; b == nil || b.Warm || b.Progress <= 0 || b.Progress >= 1 {
		t.Errorf("warm-up %+v", b)
	}
}

// Flagged, late and non-numeric readings are neither learned nor judged, nor those just after a
// change of state; counters are skipped.
func TestIgnored(t *testing.T) {
	r := newRig(t, nil)
	r.state("running")
	// the first seconds after a change of state are left out
	r.read("temp", 80)
	r.now = r.now.Add(10 * time.Second)
	r.read("temp", 50)
	r.d.Add(tags.Tag{Company: "c", Plant: "p", Asset: "M1", Field: "temp", Value: 900.0, Q: tags.FlagRange, At: r.now})
	r.d.Add(tags.Tag{Company: "c", Plant: "p", Asset: "M1", Field: "temp", Value: 900.0, At: r.now.Add(-time.Hour)})
	r.d.Add(tags.Tag{Company: "c", Plant: "p", Asset: "M1", Field: "temp", Value: "hot", At: r.now})
	r.read("mcount", 7)
	// a gauge a person read off at the machine
	r.d.Add(tags.Tag{Company: "c", Plant: "p", Asset: "M1", Field: "gauge_temp", Value: 62.0, Source: "manual", At: r.now})
	m, _ := r.d.Machine("c", "p", "M1")
	if len(m.Fields) != 1 || m.Fields[0].Now.N != 1 || m.Fields[0].Now.Mean != 50 {
		t.Fatalf("learned %+v", m.Fields)
	}
}

// Normals survive a restart: the machine is judged at once, without warming up again; a reset
// forgets them, there and on disk.
func TestRestart(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := newRig(t, st)
	r.state("running")
	r.run("vib", time.Hour, 0.2, flat(4))
	if err := r.d.Close(); err != nil {
		t.Fatal(err)
	}

	st, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	r2 := newRig(t, st)
	r2.now = r.now
	m, ok := r2.d.Machine("c", "p", "M1")
	if !ok || len(m.Fields) != 1 || len(m.Fields[0].States) != 1 || !m.Fields[0].States[0].Warm || math.Abs(m.Fields[0].States[0].Mean-4) > 0.05 {
		t.Fatalf("after a restart: %+v", m)
	}
	// a reading before the machine says its state again is not taken for a state of its own
	r2.read("vib", 4)
	if m, _ := r2.d.Machine("c", "p", "M1"); len(m.Fields[0].States) != 1 {
		t.Errorf("states after a reading without one: %+v", m.Fields[0].States)
	}
	r2.state("running")
	// back after the restart, the first readings are let settle; once near normal it is judged
	// at once, without warming up again
	r2.run("vib", 30*time.Second, 0.2, flat(4))
	r2.run("vib", 2*time.Minute, 0.2, flat(5))
	if len(r2.hints()) != 1 {
		t.Fatal("not judged after a restart")
	}
	r2.d.Flush()
	if rec, _ := r2.d.Recent("c", 10); len(rec) != 1 || rec[0].Ended != nil {
		t.Fatalf("recent %+v", rec)
	}
	if ok, err := r2.d.Reset("c", "p", "M1"); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if len(r2.hints()) != 0 {
		t.Error("a reset left the hint")
	}
	if rec, _ := r2.d.Recent("c", 10); len(rec) != 1 || rec[0].Ended == nil {
		t.Errorf("the reset did not end the hint: %+v", rec)
	}
	r2.d.Close()
	st, _ = Open(dir)
	r3 := newRig(t, st)
	defer r3.d.Close()
	if _, ok := r3.d.Machine("c", "p", "M1"); ok {
		t.Error("a reset machine came back after a restart")
	}
}

// Hints and their end go into the activity log.
func TestAudit(t *testing.T) {
	trail, err := audit.Open(t.TempDir(), quiet)
	if err != nil {
		t.Fatal(err)
	}
	defer trail.Close()
	r := newRig(t, nil)
	r.d.Audit = trail
	r.state("running")
	r.run("temp", time.Hour, 1, flat(50))
	r.run("temp", 2*time.Minute, 1, flat(60))
	r.run("temp", 6*time.Minute, 1, flat(50))
	r.d.Flush()
	list, _ := trail.List(t.Context(), "c", 0, 10)
	if len(list) != 2 || list[1].Action != "anomaly" || list[0].Action != "anomaly.end" || list[1].Target != "p/M1" || list[1].Via != "anomaly" {
		t.Fatalf("activity %+v", list)
	}
}

// The hub's Tap does no allocation for a field it knows.
func TestNoAllocation(t *testing.T) {
	r := newRig(t, nil)
	r.state("running")
	r.run("temp", time.Minute, 1, flat(50))
	tg := tags.Tag{Company: "c", Plant: "p", Asset: "M1", Field: "temp", Value: 50.5, Source: "plc", At: r.now}
	if n := testing.AllocsPerRun(1000, func() { r.d.Add(tg) }); n != 0 {
		t.Errorf("%v allocations a reading", n)
	}
}

func BenchmarkAdd(b *testing.B) {
	d, _ := New(nil, SettingsFrom(configDefaults()), nil, quiet)
	now := time.Now()
	d.Now = func() time.Time { return now }
	tg := tags.Tag{Company: "c", Plant: "p", Asset: "M1", Field: "temp", Source: "plc", At: now}
	vals := []any{50.0, 51.0, 49.5, 50.5, 48.0, 52.0, 50.2}
	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		tg.Value = vals[i%len(vals)]
		d.Add(tg)
	}
}
