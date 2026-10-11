package engine

import (
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"os"
	"strings"
	"testing"
	"time"
)

// Production history says how much of each hour the equipment reported: a press that sent its
// readings for part of the hour is mixed, with the share it was measured; a machine that sent
// nothing is simulated; a line is measured by the share of its machine-seconds; an inspection
// station by the share of readings the equipment reported.
func TestHistorySource(t *testing.T) {
	if testing.Short() {
		t.Skip("waits for live data to go stale (20 s)")
	}
	if _, err := os.Stat("js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	e, err := NewOffline(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	const c, plant, press = "huaduan", "p1", "HP-201"
	// a minute into an hour, so everything below falls in one hour's rows
	start := time.Now().Truncate(time.Hour).Add(-2*time.Hour + time.Minute)
	if !e.HistoryOn(c, start) {
		t.Fatal("no history for", c)
	}
	call := func(fn string, args ...any) {
		t.Helper()
		if _, err := e.call(fn, args...); err != nil {
			t.Fatal(fn, err)
		}
	}
	// an inspection station: one with a measured feature
	var gauge string
	for _, m := range e.Machines(c, plant) {
		if m.Measured {
			gauge = m.ID
			break
		}
	}
	if gauge == "" {
		t.Fatal("no measuring station in", plant)
	}

	// the press and the gauge report for 1200 s of plant time (well within their 20 s of liveness)
	call("ingest", c, plant, press, "temp", 80.0, "test")
	call("ingest", c, plant, gauge, "temp", 40.0, "test")
	reported := 0
	for i := 0; i < 12; i++ {
		call("fastForward", c, 100.0)
		call("ingest", c, plant, gauge, "measure", 22.0, "test")
		reported++
	}
	// then go quiet: the simulation takes over for another 600 s
	time.Sleep(21 * time.Second)
	call("fastForward", c, 600.0)

	var batch map[string]struct {
		MachineHours []struct {
			Machine                    string
			Run, Idle, Blocked, Fault  float64
			Maint, Changeover, Stopped float64
			Measured, MeasuredShare    float64
			Source                     string
		}
		LineHours []struct {
			Line                string
			MachineS, MeasuredS float64
			MeasuredShare       float64
			Source              string
		}
		Quality []struct {
			Machine       string
			N, Measured   int
			MeasuredShare float64
			Source        string
		}
	}
	if err := json.Unmarshal([]byte(e.History(c, true)), &batch); err != nil {
		t.Fatal(err)
	}
	b := batch[plant]
	if len(b.MachineHours) == 0 {
		t.Fatal("no machine hours")
	}
	var pressLine string
	for _, r := range b.MachineHours {
		total := r.Run + r.Idle + r.Blocked + r.Fault + r.Maint + r.Changeover + r.Stopped
		switch {
		case r.Machine == plant+"-"+press:
			// 1200 of the 1800 s, give or take a step at each edge
			if r.Source != "mixed" || math.Abs(r.Measured-1200) > 2 || math.Abs(r.MeasuredShare-r.Measured/total) > 0.002 {
				t.Errorf("the press: %+v (of %.0f s)", r, total)
			}
		case r.Machine == plant+"-"+gauge:
			if r.Source != "mixed" {
				t.Errorf("the gauge's hour: %+v", r)
			}
		default:
			if r.Source != "simulated" || r.Measured != 0 || r.MeasuredShare != 0 {
				t.Errorf("a machine that reported nothing: %+v", r)
			}
		}
	}
	// the press's line: its share of the line's machine-seconds
	lineOf := map[string]bool{}
	for _, l := range b.LineHours {
		if l.MeasuredS > 0 {
			lineOf[l.Line] = true
			if l.Source != "mixed" || math.Abs(l.MeasuredShare-l.MeasuredS/l.MachineS) > 0.002 || l.MeasuredShare <= 0 || l.MeasuredShare >= 1 {
				t.Errorf("a line with a reporting machine: %+v", l)
			}
			pressLine = l.Line
		} else if l.Source != "simulated" {
			t.Errorf("a line with nothing reported: %+v", l)
		}
	}
	if pressLine == "" || !strings.HasPrefix(pressLine, plant+"-") {
		t.Errorf("no line has measured time: %+v", b.LineHours)
	}
	// the gauge: the readings it reported among all it took that hour
	found := false
	for _, q := range b.Quality {
		if q.Machine != plant+"-"+gauge {
			if q.Measured != 0 || q.Source != "simulated" {
				t.Errorf("an inspection nobody reported: %+v", q)
			}
			continue
		}
		found = true
		if q.Measured != reported || q.Source != "mixed" || math.Abs(q.MeasuredShare-float64(reported)/float64(q.N)) > 0.002 {
			t.Errorf("the gauge's readings: %+v (reported %d)", q, reported)
		}
	}
	if !found {
		t.Errorf("no quality row for the gauge %s", gauge)
	}
}
