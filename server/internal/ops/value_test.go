package ops

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"smartfactory/internal/audit"
	"smartfactory/internal/calib"
	"smartfactory/internal/erp"
	"smartfactory/internal/guard"
	"smartfactory/internal/history"
)

var now = time.Date(2026, 10, 12, 10, 0, 0, 0, shanghai)

func period(t *testing.T) Period {
	t.Helper()
	p, err := ParsePeriod("30", "", "", now)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func figures(t *testing.T, fs []Figure) map[string]Figure {
	t.Helper()
	out := map[string]Figure{}
	for _, f := range fs {
		if _, dup := out[f.Key]; dup {
			t.Errorf("two figures %s", f.Key)
		}
		if f.Basis == "" {
			t.Errorf("%s says nothing of what it is counted from", f.Key)
		}
		out[f.Key] = f
	}
	return out
}

// want checks a figure's value (NaN: absent) and n.
func want(t *testing.T, fs map[string]Figure, key string, v float64, n int) {
	t.Helper()
	f, ok := fs[key]
	switch {
	case !ok:
		t.Errorf("no figure %s", key)
	case math.IsNaN(v) && f.Value != nil:
		t.Errorf("%s = %v, want 没有数据", key, *f.Value)
	case !math.IsNaN(v) && (f.Value == nil || math.Abs(*f.Value-v) > 1e-6):
		t.Errorf("%s = %s, want %v", key, f.Text(), v)
	case f.N != n:
		t.Errorf("%s n = %d, want %d", key, f.N, n)
	}
}

var none = math.NaN()

func TestParsePeriod(t *testing.T) {
	p := period(t)
	if p.Days != 30 || !p.To.Equal(now) || p.To.Sub(p.From) != 30*24*time.Hour || p.Label != "近 30 天" {
		t.Errorf("default: %+v", p)
	}
	p, err := ParsePeriod("", "2026-09-01", "2026-09-30", now)
	if err != nil || p.Days != 30 || p.From.Format("2006-01-02 15:04") != "2026-09-01 00:00" || p.To.Format("2006-01-02") != "2026-10-01" {
		t.Errorf("custom: %+v %v", p, err)
	}
	for _, bad := range [][3]string{{"0", "", ""}, {"400", "", ""}, {"x", "", ""}, {"", "2026-09-01", ""}, {"", "2026-09-30", "2026-09-01"}, {"", "2025-01-01", "2026-09-01"}, {"", "9/1", "9/30"}} {
		if _, err := ParsePeriod(bad[0], bad[1], bad[2], now); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}

// With nothing to read from, every figure is there, without a value, and each part says why.
func TestValueWithoutData(t *testing.T) {
	p := period(t)
	s := suggestionsOf(nil, p, ErrNoAdvisor.Error())
	ai := aiOf(nil, "这台服务器不记操作（没有数据目录）", nil, "这台服务器没有指令把关（没有数据目录）")
	c := calibOf(nil, nil, "x", p, "这台服务器没有存设备读数")
	o := ordersOf(nil, p, now, "这台服务器没有接 ERP")
	pl := plantOf(nil, p, nil, now, "这家公司没有接历史库")
	for name, sec := range map[string]Section{"suggestions": s.Section, "ai": ai, "calibration": c.Section, "orders": o, "plant": pl.Section} {
		if sec.Missing == "" {
			t.Errorf("%s does not say why it has no data", name)
		}
		if len(sec.Figures) == 0 {
			t.Errorf("%s has no figures", name)
		}
		for _, f := range figures(t, sec.Figures) {
			if f.Value != nil || f.Text() != "没有数据" {
				t.Errorf("%s: %s = %s with nothing to count from", name, f.Key, f.Text())
			}
		}
	}
	if s.Plans == nil || c.Fields == nil || pl.Weeks == nil || pl.Compare != nil {
		t.Error("lists must be empty, not null, and there is nothing to compare")
	}

	// sources that exist but hold nothing: counts are zero, rates and figures from data absent
	s = suggestionsOf(&board{}, p, "")
	fs := figures(t, s.Figures)
	want(t, fs, "plans.proposed", 0, 0)
	want(t, fs, "plans.adoption", none, 0)
	want(t, fs, "plans.decision_time", none, 0)
	fs = figures(t, ordersOf([]erp.Order{}, p, now, "").Figures)
	want(t, fs, "orders.received", 0, 0)
	want(t, fs, "orders.on_time_rate", none, 0)
	empty := plantOf([]history.Day{}, p, nil, now, "")
	fs = figures(t, empty.Figures)
	want(t, fs, "plant.oee", none, 0)
	want(t, fs, "plant.output", none, 0)
	want(t, fs, "plant.measured", none, 0)
	if empty.Source != "" || len(empty.Weeks) != 6 || empty.Weeks[0].OEE != nil {
		t.Errorf("an empty history: source %q, %d weeks", empty.Source, len(empty.Weeks))
	}
	v := &Value{Name: "测试", Period: p, At: now, Suggestions: s, AI: ai, Calibration: c, Orders: o, Plant: pl}
	csv := string(v.CSV())
	if !strings.HasPrefix(csv, "\xef\xbb\xbf") || !strings.Contains(csv, "没有数据") || !strings.Contains(csv, "这家公司没有接历史库") {
		t.Errorf("export:\n%s", csv)
	}
}

const boardJSON = `{"at":"2026-10-12T10:00:00+08:00","goals":[{"goal":{"id":"G1"},"findings":[],"plans":[
 {"plan":{"id":"P-A","does":"压力机换模点提前到 85%","at":"2026-09-20T09:00:00+08:00","expect":{"metric":"oee","direction":"up","by":0.05,"within_days":14}},
  "state":"adopted","decision":{"plan":"P-A","verdict":"adopted","who":"厂长","at":"2026-09-21T09:00:00+08:00","baseline":{"metric":"oee","value":0.6,"data_days":14}},
  "progress":{"plan":"P-A","final":false,"metric":"oee","baseline":0.6,"target":0.63,"measured":{"metric":"oee","value":0.66,"data_days":10},"outcome":"achieved","at":"2026-10-05T09:00:00+08:00"}},
 {"plan":{"id":"P-B","does":"夜班减一条线","at":"2026-09-25T09:00:00+08:00","expect":{"metric":"produced_units","direction":"up","by":0.1,"within_days":7}},
  "state":"rejected","decision":{"plan":"P-B","verdict":"rejected","who":"厂长","at":"2026-09-26T21:00:00+08:00"}},
 {"plan":{"id":"P-C","does":"增加一次巡检","at":"2026-10-01T09:00:00+08:00","expect":{"metric":"fault_hours","direction":"down","by":0.1,"within_days":7}},"state":"proposed"},
 {"plan":{"id":"P-E","does":"以后才采纳","at":"2026-10-10T09:00:00+08:00","expect":{"metric":"oee","direction":"up","by":0.1,"within_days":7}},
  "state":"adopted","decision":{"plan":"P-E","verdict":"adopted","who":"厂长","at":"2026-10-20T09:00:00+08:00"}}
]}],
"loose_plans":[
 {"plan":{"id":"P-D","does":"降低废品率","at":"2026-08-01T09:00:00+08:00","expect":{"metric":"scrap_rate","direction":"down","by":0.2,"within_days":30}},
  "state":"accepted","decision":{"plan":"P-D","verdict":"adopted","who":"厂长","at":"2026-08-03T09:00:00+08:00","baseline":{"metric":"scrap_rate","value":0.05,"data_days":30}},
  "acceptance":{"plan":"P-D","final":true,"metric":"scrap_rate","baseline":0.05,"target":0.04,"measured":{"metric":"scrap_rate","value":0.045,"data_days":30},"outcome":"missed","at":"2026-09-20T09:00:00+08:00"}}
]}`

func testBoard(t *testing.T) *board {
	t.Helper()
	var b board
	if err := json.Unmarshal([]byte(boardJSON), &b); err != nil {
		t.Fatal(err)
	}
	return &b
}

// Plans count by when each thing happened: proposals proposed in the period, decisions made in
// it; an adopted plan is listed when it was adopted or measured in the period.
func TestSuggestions(t *testing.T) {
	p := period(t)
	s := suggestionsOf(testBoard(t), p, "")
	fs := figures(t, s.Figures)
	want(t, fs, "plans.proposed", 4, 4) // A B C E (E is adopted after the period)
	want(t, fs, "plans.adopted", 1, 1)
	want(t, fs, "plans.rejected", 1, 1)
	want(t, fs, "plans.adoption", 0.5, 2)
	want(t, fs, "plans.decision_time", 30, 2) // 24 h and 36 h
	want(t, fs, "plans.waiting", 1, 1)
	want(t, fs, "plans.measured", 2, 2)
	want(t, fs, "plans.better", 1, 2) // A rose and D fell, as each wanted, though D missed its target
	want(t, fs, "plans.accepted", 1, 1)
	want(t, fs, "plans.achieved", 0, 1)
	if len(s.Plans) != 2 {
		t.Fatalf("plans: %+v", s.Plans)
	}
	a, d := s.Plans[0], s.Plans[1]
	if a.ID != "P-A" || a.Label != "OEE" || a.Unit != "%" || a.Accepted || *a.Measured != 0.66 || math.Abs(*a.Change-0.1) > 1e-9 || !*a.Better || a.DataDays != 10 {
		t.Errorf("P-A: %+v", a)
	}
	if d.ID != "P-D" || !d.Accepted || d.Outcome != "missed" || !*d.Better || math.Abs(*d.Change+0.1) > 1e-9 {
		t.Errorf("P-D: %+v", d)
	}
	first := firstAdopted(testBoard(t), p.To)
	if first == nil || first.plan != "P-D" {
		t.Errorf("first adopted: %+v", first)
	}
	if f := firstAdopted(testBoard(t), time.Date(2026, 8, 1, 0, 0, 0, 0, shanghai)); f != nil {
		t.Errorf("nothing was adopted before August, got %+v", f)
	}
}

func TestAIActions(t *testing.T) {
	counts := []audit.Count{
		{Action: "agent.propose", Via: "assistant", OK: true, N: 6},
		{Action: "agent.confirm", Via: "page", OK: true, N: 3},
		{Action: "agent.confirm", Via: "cli", OK: false, N: 1},
		{Action: "agent.dismiss", Via: "page", OK: true, N: 1},
		{Action: "command", Via: "mcp", OK: true, N: 7},
		{Action: "command", Via: "live", OK: true, N: 50}, // a person's, not counted
		{Action: "command", Via: "assistant", OK: false, N: 1},
		{Action: "command.refused", Via: "mcp", OK: false, N: 2},
		{Action: "command.refused", Via: "live", OK: false, N: 4},
	}
	t0 := now.Add(-48 * time.Hour)
	at := func(m int) *time.Time { x := t0.Add(time.Duration(m) * time.Minute); return &x }
	reqs := []guard.Request{
		{Status: "sent", Created: t0, DecidedBy: "王工", DecidedAt: at(4)},
		{Status: "sent", Created: t0, DecidedBy: "王工", DecidedAt: at(10)},
		{Status: "denied", Created: t0, DecidedBy: "王工", DecidedAt: at(20)},
		{Status: "refused", Created: t0, DecidedBy: "王工", DecidedAt: at(6)},
		{Status: "expired", Created: t0, DecidedAt: at(15)},
		{Status: "waiting", Created: t0},
	}
	s := aiOf(counts, "", reqs, "")
	fs := figures(t, s.Figures)
	want(t, fs, "agent.proposed", 6, 6)
	want(t, fs, "agent.confirmed", 3, 3)
	want(t, fs, "agent.dismissed", 1, 1)
	want(t, fs, "agent.confirm_rate", 0.6, 5)
	want(t, fs, "agent.open", 1, 1)
	want(t, fs, "commands.direct", 7, 7)
	want(t, fs, "commands.held", 6, 6)
	want(t, fs, "commands.approved", 2, 2)
	want(t, fs, "commands.denied", 1, 1)
	want(t, fs, "commands.expired", 1, 1)
	want(t, fs, "commands.waiting", 1, 1)
	want(t, fs, "commands.decision_time", 8, 4) // 4, 6, 10, 20
	want(t, fs, "commands.refused", 3, 3)
	want(t, fs, "commands.failed", 1, 1)
	if !strings.Contains(s.Note, "1 条确认了但没执行成功") {
		t.Errorf("note: %q", s.Note)
	}

	// the guard's requests unreadable: what the activity log says still counts
	s = aiOf(counts, "", nil, "这台服务器没有指令把关（没有数据目录）")
	fs = figures(t, s.Figures)
	want(t, fs, "commands.held", none, 0)
	want(t, fs, "commands.refused", 2, 2)
	want(t, fs, "agent.proposed", 6, 6)
	if s.Missing == "" {
		t.Error("no word on the missing requests")
	}
}

func TestCalibration(t *testing.T) {
	p := period(t)
	in := now.Add(-5 * 24 * time.Hour)
	old := now.Add(-60 * 24 * time.Hour)
	ms := []calib.Machine{
		{Asset: "HP-101", AppliedAt: &in, FittedAt: &in, Replay: []calib.Replay{{Field: "power", Before: 6, After: 2, N: 100}, {Field: "temp", Before: 4, After: 1, N: 50}}},
		{Asset: "HP-102", AppliedAt: &old, FittedAt: &old, Replay: []calib.Replay{{Field: "power", Before: 4, After: 2, N: 80}}},
		{Asset: "IH-101", FittedAt: &in, Replay: []calib.Replay{{Field: "power", Before: 100, After: 1, N: 10}}},
		{Asset: "RR-101"},
	}
	counts := []audit.Count{{Action: "calib.apply", Via: "page", OK: true, N: 2}, {Action: "calib.apply", Via: "page", OK: false, N: 1}, {Action: "calib.revert", Via: "cli", OK: true, N: 1}}
	c := calibOf(ms, counts, "", p, "")
	fs := figures(t, c.Figures)
	want(t, fs, "calib.in_force", 2, 2)
	want(t, fs, "calib.applied", 1, 1)
	want(t, fs, "calib.fitted", 2, 2)
	want(t, fs, "calib.apply", 2, 2)
	want(t, fs, "calib.revert", 1, 1)
	if len(c.Fields) != 2 || c.Fields[0].Field != "temp" || c.Fields[1].Field != "power" {
		t.Fatalf("fields: %+v", c.Fields)
	}
	// only the machines in force: IH-101's fit is not in force
	if pw := c.Fields[1]; pw.Before != 5 || pw.After != 2 || pw.Machines != 2 || pw.N != 180 {
		t.Errorf("power: %+v", pw)
	}

	// nothing in force: the fits of the period, said to be not in force
	c = calibOf(ms[2:], nil, "", p, "")
	if len(c.Fields) != 1 || c.Fields[0].Before != 100 || !strings.Contains(c.Note, "还没应用") {
		t.Errorf("fits only: %+v %q", c.Fields, c.Note)
	}
	// no activity log: the counts of applies are unknown, not zero
	fs = figures(t, calibOf(ms, nil, "没有数据目录", p, "").Figures)
	want(t, fs, "calib.apply", none, 0)
	want(t, fs, "calib.in_force", 2, 2)
}

func TestOrders(t *testing.T) {
	p := period(t)
	day := func(d int) *time.Time { x := now.AddDate(0, 0, d); return &x }
	orders := []erp.Order{
		{No: "1", Status: erp.Done, Created: *day(-20), Due: day(-10), Finished: day(-11)},
		{No: "2", Status: erp.Done, Created: *day(-20), Due: day(-10), Finished: day(-9)},
		{No: "3", Status: erp.Done, Created: *day(-50), Due: day(-40), Finished: day(-5)},  // late, finished in the period
		{No: "4", Status: erp.Done, Created: *day(-50), Due: day(-40), Finished: day(-39)}, // finished before the period
		{No: "5", Status: erp.Done, Created: *day(-3), Finished: day(-1)},                  // no due date
		{No: "6", Status: erp.Running, Created: *day(-60), Due: day(-2)},                   // overdue, open
		{No: "7", Status: erp.Queued, Created: *day(-1), Due: day(5)},
	}
	s := ordersOf(orders, p, now, "")
	fs := figures(t, s.Figures)
	want(t, fs, "orders.received", 4, 4)
	want(t, fs, "orders.finished", 4, 4)
	want(t, fs, "orders.on_time", 1, 1)
	want(t, fs, "orders.late", 2, 2)
	want(t, fs, "orders.on_time_rate", 1.0/3, 3)
	want(t, fs, "orders.overdue", 1, 1)
	if !strings.Contains(s.Note, "1 张") {
		t.Errorf("note: %q", s.Note)
	}
}

func date(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

// The plant counts measured hours only once there are any; before, the simulated ones, said to
// be simulated. The weeks run Monday on; before and after the first adopted plan are whole days.
func TestPlant(t *testing.T) {
	p := period(t)
	sim := func(d time.Time) history.Day {
		return history.Day{Day: d, Source: "simulated", LineHours: 24, PlannedS: 86400, GoodIdealS: 43200, Produced: 1000, Good: 900, MachineHours: 48, DownS: 7200}
	}
	real := func(d time.Time, oee float64) history.Day {
		return history.Day{Day: d, Source: "measured", LineHours: 24, PlannedS: 86400, GoodIdealS: 86400 * oee, Produced: 800, Good: 700, MachineHours: 48, DownS: 3600}
	}
	// simulated only
	var days []history.Day
	for d := date(2026, 9, 20); d.Before(date(2026, 10, 12)); d = d.AddDate(0, 0, 1) {
		days = append(days, sim(d))
	}
	v := plantOf(days, p, nil, now, "")
	fs := figures(t, v.Figures)
	if v.Source != "simulated" || !strings.Contains(v.Note, "不代表真实工厂") {
		t.Errorf("simulated: %q %q", v.Source, v.Note)
	}
	want(t, fs, "plant.oee", 0.5, 22*24)
	want(t, fs, "plant.measured", 0, 22*24)
	if fs["plant.oee"].Source != "simulated" {
		t.Error("the OEE does not say it is simulated")
	}

	// measured days among them: only those count; the first adoption on Oct 2 splits them
	days = days[:0]
	for d := date(2026, 9, 20); d.Before(date(2026, 10, 12)); d = d.AddDate(0, 0, 1) {
		days = append(days, sim(d))
		if !d.Before(date(2026, 9, 28)) {
			oee := 0.6
			if d.After(date(2026, 10, 2)) {
				oee = 0.7
			}
			days = append(days, real(d, oee))
		}
	}
	first := &adoption{plan: "P-D", does: "降低废品率", at: time.Date(2026, 10, 2, 15, 0, 0, 0, shanghai)}
	v = plantOf(days, p, first, now, "")
	fs = figures(t, v.Figures)
	if v.Source != "measured" {
		t.Errorf("source %q", v.Source)
	}
	want(t, fs, "plant.output", 14*800, 14*24)
	want(t, fs, "plant.downtime", 14, 14*48)
	want(t, fs, "plant.measured", 0.389, 36*24) // 14 measured days of 36 line-days
	if fs["plant.oee"].Value == nil || *fs["plant.oee"].Value <= 0.6 || *fs["plant.oee"].Value >= 0.7 {
		t.Errorf("oee: %s", fs["plant.oee"].Text())
	}
	// weeks: Sep 7 (the period starts Sep 12) to Oct 12; the ones before Sep 28 have no measured hours
	if len(v.Weeks) != 6 || v.Weeks[0].Start.Format("2006-01-02") != "2026-09-07" || v.Weeks[0].Start.Weekday() != time.Monday {
		t.Fatalf("weeks: %+v", v.Weeks)
	}
	if v.Weeks[0].Days != 2 || v.Weeks[2].OEE != nil || v.Weeks[2].Measured == nil || *v.Weeks[2].Measured != 0 {
		t.Errorf("early weeks: %+v %+v", v.Weeks[0], v.Weeks[2])
	}
	if w := v.Weeks[3]; w.OEE == nil || *w.OEE != 0.629 || w.LineHours != 7*24 || *w.Measured != 0.5 {
		t.Errorf("week of Sep 28: %+v", w)
	}
	c := v.Compare
	if c == nil {
		t.Fatal("no comparison")
	}
	// 10 days since the adoption: Sep 22–Oct 1 against Oct 3–12, measured from Sep 28 on
	if c.Days != 10 || c.Source != "measured" || c.Before.Days != 4 || c.After.Days != 9 || *c.Before.OEE != 0.6 || *c.After.OEE != 0.7 ||
		*c.Before.OutputPerDay != 800 || *c.After.DowntimePerDay != 1 || !strings.Contains(c.Note, "不证明") {
		t.Errorf("compare: %+v", c)
	}
	if c.Before.From.Format("2006-01-02") != "2026-09-22" || c.After.To.Format("2006-01-02") != "2026-10-13" {
		t.Errorf("compare sides: %s – %s", c.Before.From, c.After.To)
	}

	// measured only after the adoption: the comparison falls back to all the data and says so
	v = plantOf(append(days[:0:0], sim(date(2026, 10, 1)), sim(date(2026, 10, 3)), real(date(2026, 10, 4), 0.7)), p, first, now, "")
	if c := v.Compare; c.Source != "simulated" || !strings.Contains(c.Note, "不代表真实工厂") || c.Before.Days != 1 || c.After.Days != 2 {
		t.Errorf("one-sided measurements: %+v", c)
	}
	// adopted today: nothing after it yet
	v = plantOf(days, p, &adoption{at: now.Add(-time.Hour)}, now, "")
	if c := v.Compare; c.Days != 0 || c.Before.OEE != nil || !strings.Contains(c.Note, "没法比") {
		t.Errorf("adopted today: %+v", c)
	}
}

func TestFigureText(t *testing.T) {
	v := func(x float64) *float64 { return &x }
	for _, c := range []struct {
		f    Figure
		want string
	}{
		{Figure{Value: v(0.6123), Unit: "%"}, "61.2%"},
		{Figure{Value: v(12), Unit: "条"}, "12 条"},
		{Figure{Value: v(3.25), Unit: "h"}, "3.25 h"},
		{Figure{Value: v(31.26), Unit: "min"}, "31.3 min"},
		{Figure{Unit: "%"}, "没有数据"},
	} {
		if got := c.f.Text(); got != c.want {
			t.Errorf("%+v: %q, want %q", c.f, got, c.want)
		}
	}
}
