package series

import (
	"io"
	"log/slog"
	"math"
	"math/rand/v2"
	"testing"
	"time"

	"smartfactory/internal/config"
	"smartfactory/internal/sfconv"
	"smartfactory/internal/tags"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestChunkRoundTrip(t *testing.T) {
	base := time.Date(2026, 10, 11, 8, 0, 0, 0, time.UTC).UnixMilli()
	var ps []Point
	v := 60.0
	for i := range 3600 {
		v += rand.NormFloat64() * 0.1
		if i%100 == 0 {
			v = 60 // repeats cost a byte
		}
		p := Point{T: base + int64(i)*1000 + int64(rand.IntN(5)), V: math.Round(v*10) / 10, Src: 1}
		if i == 77 {
			p.Q = tags.FlagSpike
		}
		if i >= 2000 {
			p.Src = 2
		}
		ps = append(ps, p)
	}
	data := encodePoints(base, ps)
	got, err := decodePoints(base, data)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(ps) {
		t.Fatalf("%d points back, want %d", len(got), len(ps))
	}
	for i := range ps {
		if got[i] != ps[i] {
			t.Fatalf("point %d: %+v, want %+v", i, got[i], ps[i])
		}
	}
	if per := float64(len(data)) / float64(len(ps)); per > 4 {
		t.Errorf("%.1f bytes a reading", per)
	}
	t.Logf("%d readings in %d bytes (%.2f bytes each)", len(ps), len(data), float64(len(data))/float64(len(ps)))
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func open(t *testing.T, dir string, ret Retention, c *clock) *Store {
	t.Helper()
	s, err := Open(dir, ret, quiet)
	if err != nil {
		t.Fatal(err)
	}
	s.Now = c.now
	return s
}

func tag(at time.Time, field string, v any) tags.Tag {
	return tags.Tag{Company: "c", Plant: "p1", Asset: "HP-101", Field: field, Value: v, Source: "plc", At: at}
}

// Readings go in raw, are sealed once their hour is over, read back raw, by minute and by hour,
// and survive a restart; a late reading for a sealed hour is merged in; states are kept when
// they change.
func TestStoreSealAndRead(t *testing.T) {
	dir := t.TempDir()
	start := time.Date(2026, 10, 11, 8, 0, 0, 0, time.UTC)
	c := &clock{t: start}
	s := open(t, dir, Retention{Raw: 14 * 24 * time.Hour, Minute: 90 * 24 * time.Hour, Hour: 5 * 365 * 24 * time.Hour}, c)
	for i := range 7200 { // two hours, one a second
		s.Add(tag(start.Add(time.Duration(i)*time.Second), "temp", 60+float64(i%60)))
	}
	bad := tag(start.Add(30*time.Minute+500*time.Millisecond), "temp", 9999.0)
	bad.Q = tags.FlagRange
	s.Add(bad)
	for _, st := range []string{"running", "running", "fault", "fault", "running"} {
		s.Add(tag(start.Add(time.Minute), "state", st))
		start = start.Add(time.Second)
	}
	start = start.Add(-5 * time.Second)
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	c.t = start.Add(2*time.Hour + 5*time.Minute)
	s.Upkeep()
	var tail, raw int
	s.db.QueryRow(`SELECT count(*) FROM tail`).Scan(&tail)
	s.db.QueryRow(`SELECT count(*) FROM raw`).Scan(&raw)
	if tail != 0 || raw != 2 {
		t.Fatalf("after sealing: %d in the tail, %d raw chunks; want 0 and 2", tail, raw)
	}

	r, err := s.Read(Query{Company: "c", Plant: "p1", Asset: "HP-101", Fields: []string{"temp"}, From: start.Add(29 * time.Minute), To: start.Add(31 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if r.Resolution != Raw || len(r.Series[0].Points) != 122 {
		t.Fatalf("raw: %s with %d points, want raw with 122", r.Resolution, len(r.Series[0].Points))
	}
	flagged := 0
	for _, p := range r.Series[0].Points {
		if p[2].(uint8) != 0 {
			flagged++
		}
	}
	if flagged != 1 {
		t.Errorf("%d flagged raw readings, want 1", flagged)
	}

	r, _ = s.Read(Query{Company: "c", Plant: "p1", Asset: "HP-101", Fields: []string{"temp"}, From: start, To: start.Add(2 * time.Hour), Step: Minute})
	if len(r.Series[0].Points) != 120 {
		t.Fatalf("%d minutes, want 120", len(r.Series[0].Points))
	}
	m := r.Series[0].Points[30]
	if m[1].(float64) != 89.5 || m[2].(float64) != 60 || m[3].(float64) != 119 || m[4].(int) != 60 || m[5].(int) != 1 {
		t.Errorf("minute 30: %v, want avg 89.5, 60…119, 60 readings, 1 flagged", m)
	}
	r, _ = s.Read(Query{Company: "c", Plant: "p1", Asset: "HP-101", Fields: []string{"temp", "state"}, From: start.Add(-time.Hour), To: start.Add(3 * time.Hour), Step: Hour})
	if len(r.Series[0].Points) != 2 || r.Series[0].Points[0][4].(int) != 3600 {
		t.Fatalf("hours: %v", r.Series[0].Points)
	}
	if len(r.Events) != 3 || r.Events[1].V != "fault" || r.Events[0].Src != "plc" {
		t.Errorf("state changes: %+v, want running, fault, running", r.Events)
	}

	// a gateway replays a reading of the first hour after it was sealed
	s.Add(tag(start.Add(10*time.Minute+500*time.Millisecond), "temp", 70.0))
	s.Flush()
	r, _ = s.Read(Query{Company: "c", Plant: "p1", Asset: "HP-101", Fields: []string{"temp"}, From: start.Add(10 * time.Minute), To: start.Add(11 * time.Minute), Step: Minute})
	if r.Series[0].Points[0][4].(int) != 61 {
		t.Errorf("late reading not counted before sealing: %v", r.Series[0].Points[0])
	}
	s.Upkeep()
	r, _ = s.Read(Query{Company: "c", Plant: "p1", Asset: "HP-101", Fields: []string{"temp"}, From: start.Add(10 * time.Minute), To: start.Add(11 * time.Minute), Step: Minute})
	if r.Series[0].Points[0][4].(int) != 61 {
		t.Errorf("late reading not merged: %v", r.Series[0].Points[0])
	}

	// a restart: everything is still there, and the last state is not stored again
	s.db.Close()
	s = open(t, dir, s.ret, c)
	s.Add(tag(start.Add(2*time.Hour), "state", "running"))
	s.Flush()
	r, _ = s.Read(Query{Company: "c", Plant: "p1", Asset: "HP-101", From: start, To: start.Add(3 * time.Hour), Step: Hour})
	if len(r.Fields) != 2 || len(r.Events) != 3 {
		t.Errorf("after a restart: fields %v, %d state changes", r.Fields, len(r.Events))
	}
	s.db.Close()
}

// Raw chunks go after Raw, minutes after Minute; past MaxBytes the oldest raw hours go first.
func TestRetentionAndSize(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := &clock{t: start}
	s := open(t, t.TempDir(), Retention{Raw: 2 * 24 * time.Hour, Minute: 5 * 24 * time.Hour, Hour: 365 * 24 * time.Hour}, c)
	defer s.db.Close()
	for d := range 10 {
		for i := range 24 * 60 {
			at := start.Add(time.Duration(d)*24*time.Hour + time.Duration(i)*time.Minute)
			s.Add(tag(at, "temp", float64(i%100)))
		}
		s.Flush()
	}
	c.t = start.Add(10*24*time.Hour + time.Hour)
	s.Upkeep()
	count := func(q string) (n int) { s.db.QueryRow(q).Scan(&n); return }
	if raw := count(`SELECT count(DISTINCT hour / 86400000) FROM raw`); raw > 3 {
		t.Errorf("raw chunks on %d days, want at most 3", raw)
	}
	if mins := count(`SELECT count(DISTINCT hour / 86400000) FROM minutes`); mins > 6 {
		t.Errorf("minutes on %d days, want at most 6", mins)
	}
	if hours := count(`SELECT count(*) FROM hours`); hours != 10 {
		t.Errorf("%d days of hours, want 10", hours)
	}
	r, _ := s.Read(Query{Company: "c", Plant: "p1", Asset: "HP-101", From: start, To: c.t})
	if r.Resolution != Hour || len(r.Series[0].Points) != 240 {
		t.Errorf("ten days: %s, %d points", r.Resolution, len(r.Series[0].Points))
	}

	// a size the raw chunks do not fit in
	before := count(`SELECT count(*) FROM raw`)
	s.ret.MaxBytes = s.size() - 1
	s.Upkeep()
	if raw := count(`SELECT count(*) FROM raw`); raw >= before || before == 0 {
		t.Errorf("over the size: %d raw chunks, %d before", raw, before)
	}
	if hours := count(`SELECT count(*) FROM hours`); hours != 10 {
		t.Errorf("trimming took hours: %d left", hours)
	}
}

func TestValidator(t *testing.T) {
	lo, hi := 21.8, 22.2
	v := &Validator{
		Machines: func(c, p string) []sfconv.Machine {
			return []sfconv.Machine{{ID: "HP-101", Trip: 300, Power: 50, Measure: &sfconv.MeasureSpec{Target: 22, Lo: &lo, Hi: &hi, Sigma: 0.05}}}
		},
		Limits: []config.Limit{{Asset: "HP-*", Field: "vib", Max: ptr(8.0)}},
	}
	at := time.Now()
	cases := []struct {
		field string
		v     any
		dt    time.Duration
		q     uint8
	}{
		{"temp", 60.0, 0, 0},
		{"temp", 9999.0, time.Second, tags.FlagRange},
		{"temp", 62.0, time.Second, 0},
		{"temp", 420.0, time.Second, tags.FlagSpike}, // under trip + 200, but 358 °C in a second
		{"power", 49.0, 0, 0},
		{"power", 300.0, time.Second, tags.FlagRange},
		{"vib", 9.0, 0, tags.FlagRange},
		{"measure", 22.1, 0, 0},
		{"measure", 2200.0, 0, tags.FlagRange},
		{"health", 1.4, 0, tags.FlagRange},
		{"state", "running", 0, 0},
	}
	for _, c := range cases {
		at = at.Add(c.dt)
		tg := tags.Tag{Company: "c", Plant: "p1", Asset: "HP-101", Field: c.field, Value: c.v, At: at}
		why := v.Check(&tg)
		if tg.Q != c.q {
			t.Errorf("%s %v: flags %d (%q), want %d", c.field, c.v, tg.Q, why, c.q)
		}
		if (tg.Q != 0) != (why != "") {
			t.Errorf("%s %v: flagged without a reason or the other way round: %q", c.field, c.v, why)
		}
	}
	// a real step: after three flagged jumps in a row the new level is taken
	first := tags.Tag{Company: "c", Plant: "p1", Asset: "HP-102", Field: "temp", Value: 60.0, At: at}
	v.Check(&first)
	for i := range 5 {
		at = at.Add(time.Second)
		tg := tags.Tag{Company: "c", Plant: "p1", Asset: "HP-102", Field: "temp", Value: 250.0, At: at}
		v.Check(&tg)
		if want := i >= 3; (tg.Q == 0) != want {
			t.Errorf("step, reading %d: flags %d", i, tg.Q)
		}
	}
}

func ptr(x float64) *float64 { return &x }
