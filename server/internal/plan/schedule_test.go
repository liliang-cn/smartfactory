package plan

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"
)

// Monday 2026-11-02 08:00 China time.
var t0 = time.Date(2026, 11, 2, 8, 0, 0, 0, cst)

func line(key string, rate, setupMin float64) Line {
	return Line{Key: key, Plant: "p1", ID: key, Name: key, No: 1, Rate: rate, Neck: rate, Eff: 1, Setup: setupMin * 60, Crew: true}
}

func order(no, part string, qty int, dueH float64, lines ...string) Order {
	o := Order{No: no, Part: part, Qty: qty, Lines: lines, Created: t0}
	if dueH > 0 {
		o.Due = t0.Add(time.Duration(dueH * float64(time.Hour)))
	}
	return o
}

func byNo(r *Result) map[string]Planned {
	out := map[string]Planned{}
	for _, o := range r.Orders {
		out[o.No] = o
	}
	for _, o := range r.Held {
		out[o.No] = o
	}
	return out
}

// Two orders never overlap on a line: each starts once the one before it has ended.
func TestCapacity(t *testing.T) {
	in := &Input{Now: t0, Goal: Due, Lines: []Line{line("A", 36, 0)}, Orders: []Order{
		order("O1", "X", 100, 10, "A"), order("O2", "X", 100, 10, "A"), order("O3", "X", 100, 10, "A"),
	}}
	r := Schedule(in)
	if len(r.Orders) != 3 {
		t.Fatalf("planned %d", len(r.Orders))
	}
	for i := 1; i < len(r.Orders); i++ {
		a, b := r.Orders[i-1], r.Orders[i]
		if b.SetupStart.Before(a.End) {
			t.Errorf("%s (%s–%s) overlaps %s (%s–%s)", b.No, b.SetupStart, b.End, a.No, a.SetupStart, a.End)
		}
	}
	// 300 pieces at 36 s is 3 hours of work: the third ends 3 h after 08:00
	if end := r.Orders[2].End; !end.Equal(t0.Add(3 * time.Hour)) {
		t.Errorf("last ends %s, want 11:00", end.In(cst))
	}
}

// Meal breaks, shift handovers and days off are not worked.
func TestCalendar(t *testing.T) {
	l := line("A", 36, 0)
	l.Cal = &Calendar{Breaks: []Break{{At: 11.5, Minutes: 30}}, Shifts: []float64{8, 20}, Handover: 20, Pace: 0.5, Off: []int{0}}
	// 8 hours of work from 08:00: the first 20 minutes at half pace (10 min lost), 30 min lunch
	in := &Input{Now: t0, Lines: []Line{l}, Orders: []Order{order("O1", "X", 800, 0, "A")}}
	r := Schedule(in)
	want := t0.Add(8*time.Hour + 40*time.Minute)
	if end := r.Orders[0].End; !end.Equal(want) {
		t.Errorf("ends %s, want %s", end.In(cst), want.In(cst))
	}
	// Saturday 20:00 with a day off on Sunday: 10 hours of work end Monday
	sat := time.Date(2026, 11, 7, 20, 0, 0, 0, cst)
	tl := build(l.Cal, true, 1, nil, sat.Unix(), 5)
	end := time.Unix(tl.advance(tl.next(sat.Unix()), 10*3600), 0).In(cst)
	if end.Weekday() != time.Monday {
		t.Errorf("10 h from Saturday 20:00 with Sunday off end %s", end)
	}
	if w := tl.work(time.Date(2026, 11, 8, 0, 0, 0, 0, cst).Unix(), time.Date(2026, 11, 9, 0, 0, 0, 0, cst).Unix()); w != 0 {
		t.Errorf("Sunday has %v s of work", w)
	}
	// weekly planned maintenance, 4 h from 08:00: line n on weekday n-1, so line 2 on Monday
	pm := &Calendar{PM: &[2]float64{4, 8}}
	if got := build(pm, true, 2, nil, t0.Unix(), 2).next(t0.Unix()); got != t0.Add(4*time.Hour).Unix() {
		t.Errorf("line 2 on Monday 08:00 resumes %s, want 12:00", time.Unix(got, 0).In(cst))
	}
	if got := build(pm, true, 3, nil, t0.Unix(), 2).next(t0.Unix()); got != t0.Unix() {
		t.Errorf("line 3 stops on Monday")
	}
	// a planned stop
	stop := []Stop{{Line: "A", From: t0.Unix(), To: t0.Add(2 * time.Hour).Unix()}}
	if got := build(nil, true, 3, stop, t0.Unix(), 2).advance(t0.Unix(), 3600); got != t0.Add(3*time.Hour).Unix() {
		t.Errorf("an hour of work through a 2 h stop ends %s", time.Unix(got, 0).In(cst))
	}
}

// Orders of the same part go together: fewer changeovers than in plain due-date order.
func TestBatching(t *testing.T) {
	var orders []Order
	for i := range 12 {
		part := []string{"X", "Y", "Z"}[i%3]
		orders = append(orders, order(fmt.Sprintf("O%02d", i), part, 50, 200+float64(i), "A"))
	}
	in := &Input{Now: t0, Goal: Setups, Lines: []Line{line("A", 36, 60)}, Orders: orders}
	r := Schedule(in)
	edd := Evaluate(&Input{Now: t0, Goal: Setups, Lines: in.Lines, Orders: orders}, Seq{})
	if r.KPI.Setups >= edd.KPI.Setups || r.KPI.Setups > 2 {
		t.Errorf("setups: plan %d, due-date order %d", r.KPI.Setups, edd.KPI.Setups)
	}
	if r.KPI.Late != 0 {
		t.Errorf("%d late", r.KPI.Late)
	}
	// on 交期优先 tight due dates win over batching
	var tight []Order
	for i := range 6 {
		tight = append(tight, order(fmt.Sprintf("T%d", i), []string{"X", "Y"}[i%2], 100, float64(i+1)*1.1+1, "A"))
	}
	due := Schedule(&Input{Now: t0, Goal: Due, Lines: []Line{line("A", 36, 30)}, Orders: tight})
	setup := Schedule(&Input{Now: t0, Goal: Setups, Lines: []Line{line("A", 36, 30)}, Orders: tight})
	if due.KPI.LateHours > setup.KPI.LateHours || due.KPI.Setups < setup.KPI.Setups {
		t.Errorf("交期优先 late %.1f h with %d setups, 少换型 %.1f h with %d", due.KPI.LateHours, due.KPI.Setups, setup.KPI.LateHours, setup.KPI.Setups)
	}
}

// An order short of material is held back, or planned from when its material comes.
func TestMaterial(t *testing.T) {
	o := order("O1", "X", 100, 48, "A")
	o.Short, o.Hold = "40Cr φ80 缺 1.2 t", true
	in := &Input{Now: t0, Lines: []Line{line("A", 36, 0)}, Orders: []Order{o, order("O2", "X", 100, 48, "A")}}
	r := Schedule(in)
	if len(r.Held) != 1 || r.Held[0].No != "O1" || len(r.Orders) != 1 {
		t.Fatalf("held %v, planned %v", r.Held, r.Orders)
	}
	o.Hold, o.Release = false, t0.Add(30*time.Hour)
	r = Schedule(&Input{Now: t0, Lines: []Line{line("A", 36, 0)}, Orders: []Order{o}})
	p := byNo(r)["O1"]
	if p.Start.Before(o.Release) {
		t.Errorf("starts %s before its material comes %s", p.Start, o.Release)
	}
	if len(p.Flags) == 0 || p.Flags[0].Kind != "material" {
		t.Errorf("no 缺料 flag: %v", p.Flags)
	}
}

// One die cannot be on two lines at once: the second line waits for it.
func TestDies(t *testing.T) {
	a, b := line("A", 36, 0), line("B", 36, 0)
	a.Kinds, b.Kinds = []string{"锻模"}, []string{"锻模"}
	in := &Input{Now: t0, Lines: []Line{a, b}, Dies: []Die{{No: "M-1", Kind: "锻模", Product: "X", Life: 10000, Left: 150, Line: "A"}},
		Orders: []Order{order("O1", "X", 100, 2, "A", "B"), order("O2", "X", 100, 2, "A", "B")}}
	r := Schedule(in)
	ps := byNo(r)
	o1, o2 := ps["O1"], ps["O2"]
	if o1.Line != o2.Line && o2.SetupStart.Before(o1.End) && o1.SetupStart.Before(o2.End) {
		t.Errorf("one die on two lines at once: %s %s–%s and %s %s–%s", o1.Line, o1.Start, o1.End, o2.Line, o2.Start, o2.End)
	}
	// 150 strokes left: the second order runs out of die
	flagged := false
	for _, f := range append(o1.Flags, o2.Flags...) {
		flagged = flagged || f.Kind == "die"
	}
	if !flagged {
		t.Errorf("no die flag: %v %v", o1.Flags, o2.Flags)
	}
	// with a second die both lines run at once
	in.Dies = append(in.Dies, Die{No: "M-2", Kind: "锻模", Product: "X", Life: 10000, Left: 9000})
	ps = byNo(Schedule(in))
	if ps["O1"].Line == ps["O2"].Line || !ps["O1"].Start.Equal(ps["O2"].Start) {
		t.Errorf("two dies, still in turn: %+v %+v", ps["O1"], ps["O2"])
	}
	// a die booked to the product family counts for its part numbers
	in.Dies = []Die{{No: "M-9", Kind: "锻模", Product: "曲轴", Life: 10000, Left: 9000}}
	in.Orders[0].Family, in.Orders[1].Family = []string{"曲轴"}, []string{"曲轴"}
	ps = byNo(Schedule(in))
	if o1, o2 := ps["O1"], ps["O2"]; o2.SetupStart.Before(o1.End) && o1.SetupStart.Before(o2.End) {
		t.Errorf("family die on two lines at once")
	}
}

// A locked order keeps its line and start; the others go around it.
func TestLocked(t *testing.T) {
	lockAt := t0.Add(1 * time.Hour)
	o := order("L", "Y", 100, 0, "A", "B")
	o.Lock = &Lock{Line: "B", Start: lockAt}
	in := &Input{Now: t0, Goal: Due, Lines: []Line{line("A", 36, 0), line("B", 36, 0)}, Orders: []Order{
		order("O1", "X", 200, 3, "A", "B"), order("O2", "X", 200, 3, "A", "B"), order("O3", "X", 200, 4, "A", "B"), o,
	}}
	for _, g := range []Goal{Due, Balance, Setups} {
		in.Goal = g
		ps := byNo(Schedule(in))
		l := ps["L"]
		if l.Line != "B" || !l.SetupStart.Equal(lockAt) || !l.Locked {
			t.Errorf("%s: locked order on %s at %s", g, l.Line, l.SetupStart.In(cst))
		}
		for no, p := range ps {
			if no != "L" && p.Line == "B" && p.SetupStart.Before(l.End) && l.SetupStart.Before(p.End) {
				t.Errorf("%s: %s overlaps the locked order", g, no)
			}
		}
	}
}

// Lateness is end minus due, in minutes, and only when it ends after its due date.
func TestLateness(t *testing.T) {
	in := &Input{Now: t0, Lines: []Line{line("A", 36, 30)}, Orders: []Order{
		order("O1", "X", 100, 1.5, "A"), // 1 h of work: on time
		order("O2", "Y", 100, 2, "A"),   // 30 min changeover + 1 h after O1: ends 10:30, due 10:00
	}}
	in.Lines[0].Last = "X"
	ps := byNo(Schedule(in))
	if ps["O1"].Late || ps["O1"].LateMin != 0 {
		t.Errorf("O1 late %v", ps["O1"].LateMin)
	}
	if !ps["O2"].Late || ps["O2"].LateMin != 30 || ps["O2"].SetupMin != 30 {
		t.Errorf("O2: late %v min, setup %v", ps["O2"].LateMin, ps["O2"].SetupMin)
	}
	r := Schedule(in)
	if r.KPI.OnTime != 1 || r.KPI.Late != 1 || r.KPI.LateHours != 0.5 || r.KPI.OnTimePct != 50 || r.KPI.Setups != 1 {
		t.Errorf("kpi %+v", r.KPI)
	}
	if len(ps["O2"].Why) == 0 {
		t.Error("no reason given for O2")
	}
}

// The same input gives the same plan, every time.
func TestDeterministic(t *testing.T) {
	in := bigInput(120, 8, 3)
	a, _ := json.Marshal(Schedule(in).Orders)
	for range 3 {
		b, _ := json.Marshal(Schedule(bigInput(120, 8, 3)).Orders)
		if string(a) != string(b) {
			t.Fatal("two plans of the same input differ")
		}
	}
}

func bigInput(n, lines, seed int) *Input {
	in := &Input{Now: t0, Goal: Balance}
	cal := &Calendar{Breaks: []Break{{At: 11.5, Minutes: 30}, {At: 17.5, Minutes: 30}, {At: 0, Minutes: 30}}, Shifts: []float64{8, 20}, Handover: 20, Pace: 0.5, PM: &[2]float64{4, 8}}
	for j := range lines {
		l := line(fmt.Sprintf("p1/L%d", j+1), 20+float64(j%5)*6, 60+float64(j%3)*30)
		l.No, l.Cal, l.Kinds = j+1, cal, []string{"锻模"}
		in.Lines = append(in.Lines, l)
	}
	r := uint32(seed)
	rnd := func(k int) int { r = r*1664525 + 1013904223; return int(r>>8) % k }
	parts := 12
	for p := range parts {
		in.Dies = append(in.Dies, Die{No: fmt.Sprintf("M-%d", p), Kind: "锻模", Product: fmt.Sprintf("P%d", p), Life: 20000, Left: int64(5000 + rnd(15000))})
	}
	for i := range n {
		part := rnd(parts)
		// each part on 3 of the lines
		var ls []string
		for k := range 3 {
			ls = append(ls, in.Lines[(part+k*3)%lines].Key)
		}
		o := order(fmt.Sprintf("MO-%04d", i), fmt.Sprintf("P%d", part), 100+rnd(20)*50, float64(12+rnd(24*10)), ls...)
		o.Created = t0.Add(time.Duration(i) * time.Minute)
		in.Orders = append(in.Orders, o)
	}
	return in
}

// 300 orders on 20 lines plan in well under a second.
func BenchmarkSchedule300x20(b *testing.B) {
	for b.Loop() {
		Schedule(bigInput(300, 20, 11))
	}
}

func TestBig(t *testing.T) {
	start := time.Now()
	r := Schedule(bigInput(300, 20, 11))
	took := time.Since(start)
	base := Evaluate(bigInput(300, 20, 11), Seq{})
	t.Logf("300 orders × 20 lines: %s, %d sequences tried; on time %d/%d (due-date order on first line %d), late %.1f h (%.1f), setups %d (%d)",
		took, r.Evals, r.KPI.OnTime, r.KPI.Planned, base.KPI.OnTime, r.KPI.LateHours, base.KPI.LateHours, r.KPI.Setups, base.KPI.Setups)
	if took > time.Second {
		t.Errorf("took %s", took)
	}
	if r.KPI.Cost > base.KPI.Cost {
		t.Errorf("plan costs %.1f, plain due-date order %.1f", r.KPI.Cost, base.KPI.Cost)
	}
}
