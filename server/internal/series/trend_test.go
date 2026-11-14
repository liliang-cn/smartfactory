package series

import (
	"strings"
	"testing"
	"time"

	"smartfactory/internal/tags"
)

// A machine's three hours come back summed up: the low, high and mean of a reading and when,
// a curve of it, the minutes in each state, its stops longest first with why; one that went
// quiet is not taken to have run on; the ranking puts the machine that stood still longest
// first.
func TestTrendAndDowntime(t *testing.T) {
	start := time.Date(2026, 11, 14, 0, 0, 0, 0, time.UTC) // 08:00 in Shanghai
	c := &clock{t: start}
	s := open(t, t.TempDir(), Retention{Raw: 14 * 24 * time.Hour, Minute: 90 * 24 * time.Hour, Hour: 5 * 365 * 24 * time.Hour}, c)
	put := func(asset string, at time.Time, field string, v any) {
		s.Add(tags.Tag{Company: "c", Plant: "p1", Asset: asset, Field: field, Value: v, Source: "plc", At: at})
	}
	min := func(m int) time.Time { return start.Add(time.Duration(m) * time.Minute) }

	// HP-101: runs, a 12-minute fault at 08:30 (die too cold), runs, a 20-minute changeover at
	// 09:40, runs to 11:00; its temperature climbs from 40 to 75 and peaks during the fault
	for i := 0; i < 180*6; i++ {
		at := start.Add(time.Duration(i) * 10 * time.Second)
		v := 40 + float64(i)/float64(180*6)*30
		if at.After(min(30)) && at.Before(min(42)) {
			v = 90
		}
		put("HP-101", at, "temp", v)
		if i < 90*6 {
			put("HP-102", at, "temp", 50.0)
		}
	}
	spike := tags.Tag{Company: "c", Plant: "p1", Asset: "HP-101", Field: "temp", Value: 999.0, Source: "plc", At: min(60), Q: tags.FlagSpike}
	s.Add(spike)
	put("HP-101", min(0), "state", "running")
	put("HP-101", min(30), "state", "fault")
	put("HP-101", min(30).Add(time.Second), "fault", "模具温度过低")
	put("HP-101", min(42), "state", "running")
	put("HP-101", min(42), "fault", "")
	put("HP-101", min(50), "state", "idle")
	put("HP-101", min(52), "state", "running")
	put("HP-101", min(100), "state", "changeover")
	put("HP-101", min(120), "state", "running")
	// HP-102: one 3-minute short stop, then it stops sending at 09:30
	put("HP-102", min(0), "state", "running")
	put("HP-102", min(10), "fault", "短停：工件粘模")
	put("HP-102", min(10), "state", "fault")
	put("HP-102", min(13), "state", "running")
	put("HP-102", min(13), "fault", "")
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	c.t = min(180)
	shanghai := time.FixedZone("CST", 8*3600)

	tr, err := s.Trend(TrendQuery{Company: "c", Plant: "p1", Asset: "HP-101", Fields: []string{"temp"}, From: start, Points: 12, Loc: shanghai})
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Fields) != 1 {
		t.Fatalf("fields: %+v", tr.Fields)
	}
	temp := tr.Fields[0]
	if temp.Max == nil || temp.Max.V != 90 || temp.Max.At.Format("15:04") != "08:30" {
		t.Errorf("high: %+v, want 90 at 08:30 (the spike of 999 is not believed)", temp.Max)
	}
	if temp.Min == nil || temp.Min.V != 40 || temp.Flagged != 1 || len(temp.Curve) != 12 {
		t.Errorf("low %+v, %d flagged, %d curve points; want 40, 1, 12", temp.Min, temp.Flagged, len(temp.Curve))
	}
	st := tr.States
	if st == nil {
		t.Fatal("no states")
	}
	want := map[string]float64{"running": 146, "fault": 12, "changeover": 20, "idle": 2}
	for _, x := range st.Totals {
		if want[x.State] != x.Minutes {
			t.Errorf("%s: %.1f minutes, want %.0f", x.State, x.Minutes, want[x.State])
		}
	}
	if st.Totals[0].State != "running" || st.StopCount != 2 || st.StopMinutes != 32 {
		t.Errorf("totals %+v, %d stops for %.1f minutes; want running first, 2 stops, 32 minutes", st.Totals, st.StopCount, st.StopMinutes)
	}
	if s0 := st.Stops[0]; s0.State != "changeover" || s0.Minutes != 20 || s0.From.Format("15:04") != "09:40" {
		t.Errorf("longest stop: %+v", s0)
	}
	if s1 := st.Stops[1]; s1.Reason != "模具温度过低" || s1.Minutes != 12 {
		t.Errorf("the fault: %+v, want 12 minutes for 模具温度过低", s1)
	}
	if tr.LastReading != nil || tr.Note != "" {
		t.Errorf("a machine sending to the end is not quiet: %v %q", tr.LastReading, tr.Note)
	}

	// HP-102 went quiet after 09:30 (its readings end; its last state does not run on)
	q, err := s.Trend(TrendQuery{Company: "c", Plant: "p1", Asset: "HP-102", From: start, Loc: shanghai})
	if err != nil {
		t.Fatal(err)
	}
	if q.LastReading == nil || !strings.Contains(q.Note, "没有再发数据") {
		t.Errorf("quiet machine: last %v, note %q", q.LastReading, q.Note)
	}

	rank, err := s.Downtime("c", "", start, min(180), shanghai)
	if err != nil {
		t.Fatal(err)
	}
	if len(rank) != 2 || rank[0].Asset != "HP-101" || rank[0].StopMinutes != 32 || rank[0].ByState["changeover"] != 20 || rank[0].Longest == nil {
		t.Fatalf("ranking: %+v", rank)
	}
	if rank[1].Asset != "HP-102" || rank[1].StopCount != 1 || rank[1].Reasons[0].Reason != "短停：工件粘模" {
		t.Errorf("second: %+v", rank[1])
	}

	if _, err := s.Trend(TrendQuery{Company: "c", Plant: "p1", Asset: "HP-101", From: start.Add(-100 * 24 * time.Hour)}); err == nil {
		t.Error("a span past what is kept was answered")
	}
	if _, err := s.Trend(TrendQuery{Company: "c", Plant: "p1", Asset: "NOPE"}); err == nil {
		t.Error("an unknown machine was answered")
	}
}
