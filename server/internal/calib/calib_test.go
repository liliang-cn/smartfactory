package calib

import (
	"context"
	"io"
	"log/slog"
	"math"
	"math/rand/v2"
	"testing"
	"time"

	"smartfactory/internal/series"
	"smartfactory/internal/tags"
)

// truth is how the synthetic machine really behaves; the template says something else.
var truth = struct {
	Thermal            Thermal
	Power              Power
	Vib, Cycle         float64
	Health, Speed      float64
	FaultEvery         time.Duration // running time between breakdowns
	RepairLo, RepairHi time.Duration
}{Thermal{9, 31, 120}, Power{4.2, 22}, 2.6, 37.5, 0.9, 1, 150 * time.Minute, 2 * time.Minute, 4 * time.Minute}

var template = Template{Thermal: Thermal{4, 20, 40}, Power: Power{1, 5}, Vib: 1, Cycle: 8, MTBF: 3.33, RepairMin: 1, RepairMax: 3}

// synth plays the sim's equations for a day: 20 minutes running, 10 idle, a breakdown every
// FaultEvery of running, readings every 5 s with noise, a part counter.
func synth(start time.Time, hours float64, seed uint64) (Data, []tags.Tag) {
	rng := rand.New(rand.NewPCG(seed, 7))
	var d Data
	var ts []tags.Tag
	add := func(f string, at time.Time, v any) {
		ts = append(ts, tags.Tag{Company: "co", Plant: "p1", Asset: "HP-101", Field: f, Value: v, Source: "test", At: at})
	}
	temp := ambient + truth.Thermal.Idle
	state := ""
	var sinceFault time.Duration
	var faultEnd time.Time
	cycles, nextPart := 0.0, truth.Cycle/truth.Speed
	const dt = 5.0
	end := start.Add(time.Duration(hours * float64(time.Hour)))
	for t := start; t.Before(end); t = t.Add(5 * time.Second) {
		phase := t.Sub(start) % (30 * time.Minute)
		want := "running"
		if phase >= 20*time.Minute {
			want = "idle"
		}
		if !faultEnd.IsZero() {
			if t.Before(faultEnd) {
				want = "fault"
			} else {
				faultEnd = time.Time{}
			}
		}
		if want == "running" && sinceFault >= truth.FaultEvery {
			sinceFault = 0
			faultEnd = t.Add(truth.RepairLo + time.Duration(rng.Float64()*float64(truth.RepairHi-truth.RepairLo)))
			want = "fault"
		}
		if want != state {
			state = want
			add("state", t, state)
			d.States = append(d.States, StateChange{t, state})
		}
		m, _ := modeOf(state)
		f := 0.6 + 0.4*truth.Speed
		target := ambient
		switch m {
		case run:
			target += truth.Thermal.Run*f + (1-truth.Health)*18
			sinceFault += 5 * time.Second
			nextPart -= dt
			for nextPart <= 0 {
				cycles++
				nextPart += truth.Cycle / truth.Speed
			}
		case idle:
			target += truth.Thermal.Idle*f + (1-truth.Health)*18
		}
		temp += (target - temp) * (1 - math.Exp(-dt/truth.Thermal.Tau))
		tv := temp + rng.NormFloat64()*0.4
		pv := powerModel(truth.Power, m, false, truth.Speed) * (1 + rng.NormFloat64()*0.02)
		vv := vibModel(truth.Vib, m, truth.Speed, truth.Health) * (1 + rng.NormFloat64()*0.04)
		d.Temp = append(d.Temp, Sample{t, tv})
		d.Power = append(d.Power, Sample{t, pv})
		d.Vib = append(d.Vib, Sample{t, vv})
		d.Cycles = append(d.Cycles, Sample{t, cycles})
		add("temp", t, tv)
		add("power", t, pv)
		add("vib", t, vv)
		add("cycles", t, cycles)
		if t.Equal(start) {
			add("health", t, truth.Health)
			add("speed", t, truth.Speed)
			d.Health = append(d.Health, Sample{t, truth.Health})
			d.Speed = append(d.Speed, Sample{t, truth.Speed})
		}
	}
	return d, ts
}

func fitted(t *testing.T, ps []Param, name string) float64 {
	t.Helper()
	for _, p := range ps {
		if p.Name == name {
			if p.Fitted == nil {
				t.Fatalf("%s not fitted: %s", name, p.Note)
			}
			return *p.Fitted
		}
	}
	t.Fatalf("no parameter %s", name)
	return 0
}

func near(t *testing.T, what string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Errorf("%s = %.3f, want %.3f ± %.3f", what, got, want, tol)
	}
}

// A day of a machine that runs hotter, draws more and is slower than its template: fitting
// finds what it really does, and the replay shows the model closer to the measurements.
func TestFitRecoversTheMachine(t *testing.T) {
	d, _ := synth(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), 24, 1)
	ps, rp := Fit(d, template)
	for _, p := range ps {
		if p.Fitted != nil {
			t.Logf("%-10s template %8.2f fitted %8.2f n %6d %s", p.Name, p.Template, *p.Fitted, p.N, p.Quality)
		}
	}
	for _, x := range rp {
		t.Logf("replay %-6s before %.3f after %.3f n %d", x.Field, x.Before, x.After, x.N)
	}
	near(t, "idle rise", fitted(t, ps, "idleRise"), truth.Thermal.Idle, 0.6)
	near(t, "run rise", fitted(t, ps, "runRise"), truth.Thermal.Run, 0.8)
	near(t, "time constant", fitted(t, ps, "tau"), truth.Thermal.Tau, 0.2*truth.Thermal.Tau)
	near(t, "idle power", fitted(t, ps, "pIdle"), truth.Power.Idle, 0.1)
	near(t, "running power", fitted(t, ps, "pRun"), truth.Power.Run, 0.5)
	near(t, "vibration", fitted(t, ps, "vib"), truth.Vib, 0.08)
	near(t, "cycle", fitted(t, ps, "cycle"), truth.Cycle, 1)
	// breakdowns every 150 running minutes at 90 % health: the template's figure is at full health
	near(t, "mtbf", fitted(t, ps, "mtbf"), 2.5*(1+0.1*6), 0.5)
	near(t, "repair from", fitted(t, ps, "repairMin"), 2.5, 0.6)
	near(t, "repair to", fitted(t, ps, "repairMax"), 3.5, 0.6)
	for _, x := range rp {
		if !(x.After < x.Before/3) {
			t.Errorf("%s: model error %.3f with the template, %.3f fitted: not much better", x.Field, x.Before, x.After)
		}
	}
	if len(rp) != 3 {
		t.Errorf("replays: %+v", rp)
	}
}

// Too little to go on: nothing is fitted, and each parameter says what is missing.
func TestFitRefusesThinData(t *testing.T) {
	d, _ := synth(time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), 0.05, 2) // three minutes
	ps, _ := Fit(d, template)
	for _, p := range ps {
		if p.Fitted != nil {
			t.Errorf("%s fitted from three minutes: %v (n %d)", p.Name, *p.Fitted, p.N)
		}
		if p.Note == "" {
			t.Errorf("%s: no reason given", p.Name)
		}
	}
	ps, _ = Fit(Data{Temp: d.Temp, Power: d.Power}, template)
	for _, p := range ps {
		if p.Fitted != nil || p.Note == "" {
			t.Errorf("without states %s: %+v", p.Name, p)
		}
	}
}

// Through the series store: readings stored, fitted by the service the way the console asks.
func TestFitFromStoredReadings(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	now := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	st, err := series.Open(t.TempDir(), series.Retention{Raw: 14 * 24 * time.Hour, Minute: 90 * 24 * time.Hour, Hour: 365 * 24 * time.Hour, MaxBytes: 1 << 30}, log)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.Now = func() time.Time { return now }
	_, ts := synth(now.Add(-24*time.Hour), 24, 3)
	for i, x := range ts {
		st.Add(x)
		if i%20000 == 0 {
			st.Flush()
		}
	}
	if err := st.Flush(); err != nil {
		t.Fatal(err)
	}
	s := &Service{Series: st, Now: func() time.Time { return now }}
	d, err := s.load("co", "p1", "HP-101", now.Add(-24*time.Hour), now)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Temp) < 17000 || len(d.States) < 40 || len(d.Cycles) < 17000 {
		t.Fatalf("read back %d temperatures, %d state changes, %d counts", len(d.Temp), len(d.States), len(d.Cycles))
	}
	ps, _ := Fit(d, template)
	near(t, "run rise from the store", fitted(t, ps, "runRise"), truth.Thermal.Run, 0.8)
	near(t, "idle power from the store", fitted(t, ps, "pIdle"), truth.Power.Idle, 0.1)
	_ = context.Background()
}
