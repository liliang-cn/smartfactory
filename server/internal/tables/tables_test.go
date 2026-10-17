package tables

import (
	"context"
	"fmt"
	"testing"
	"time"

	"smartfactory/internal/engine"
	"smartfactory/internal/mapping"
	"smartfactory/internal/tags"
)

// fakeTwin is the engine's side: the tables it has, and what it was told.
type fakeTwin struct {
	tables []engine.Table
	told   []string
	sims   []engine.SimSession
}

func (f *fakeTwin) CompanyIDs() []string { return []string{"shop"} }
func (f *fakeTwin) TableList(c string) []engine.Table {
	if c != "shop" {
		return nil
	}
	return append([]engine.Table{}, f.tables...)
}
func (f *fakeTwin) ReportTable(c, plant, table, state string, people int, since time.Time, src string) bool {
	for i, t := range f.tables {
		if t.Plant == plant && t.ID == table {
			f.tables[i].State, f.tables[i].People, f.tables[i].Since, f.tables[i].Source, f.tables[i].Reported = state, people, since.UnixMilli(), src, true
			f.told = append(f.told, table+" "+state+" "+since.In(shanghai).Format("15:04"))
			return true
		}
	}
	return false
}
func (f *fakeTwin) TableSessions(string) []engine.SimSession {
	out := f.sims
	f.sims = nil
	return out
}

func newTwin() *fakeTwin {
	return &fakeTwin{tables: []engine.Table{
		{Plant: "p1", PlantName: "春熙路店", ID: "A01", Area: "大厅 A 区", Seats: 4, State: Free, Source: Sim},
		{Plant: "p1", PlantName: "春熙路店", ID: "A02", Area: "大厅 A 区", Seats: 2, State: Free, Source: Sim},
		{Plant: "p1", PlantName: "春熙路店", ID: "B01", Area: "大厅 B 区", Seats: 6, State: Free, Source: Sim},
		{Plant: "p2", PlantName: "玉林路店", ID: "A01", Area: "大厅", Seats: 4, State: Free, Source: Sim},
	}}
}

// at is a local time on the test day.
func at(hhmm string) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", "2026-10-17 "+hhmm, shanghai)
	if err != nil {
		panic(err)
	}
	return t
}

func open(t *testing.T, twin *fakeTwin, now time.Time) *Service {
	t.Helper()
	s, err := Open(t.TempDir(), twin, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	s.Now = func() time.Time { return now }
	return s
}

func TestReportBatchAndIdempotency(t *testing.T) {
	twin := newTwin()
	s := open(t, twin, at("12:30"))
	batch := []Report{
		{Table: "A01", Plant: "p1", State: Occupied, People: 3, Since: at("12:00").UnixMilli(), Source: "cam-1"},
		{Table: "A02", Plant: "p1", State: Reserved, Since: at("12:10").Format(time.RFC3339), Source: "cam-1"},
		{Table: "Z99", Plant: "p1", State: Free},
		{Table: "B01", State: "eating"},
		{Table: "A01", State: Free},
	}
	r, err := s.Report("shop", batch, "收银系统")
	if err != nil {
		t.Fatal(err)
	}
	if r.Accepted != 2 || r.Unknown != 1 || r.Invalid != 2 {
		t.Fatalf("first batch: %+v", r)
	}
	if r.Items[4].Error == "" {
		t.Error("A01 is in two plants: the report without plant should say so")
	}
	// the same again: nothing new
	r, _ = s.Report("shop", batch[:2], "收银系统")
	if r.Duplicate != 2 || r.Accepted != 0 {
		t.Fatalf("again: %+v", r)
	}
	if len(twin.told) != 2 || twin.tables[0].State != Occupied || twin.tables[0].People != 3 || twin.tables[0].Source != "cam-1" {
		t.Fatalf("twin: %v %+v", twin.told, twin.tables[0])
	}
	if _, err := s.Report("nowhere", batch, ""); err != ErrUnknownCompany {
		t.Errorf("unknown company: %v", err)
	}
}

func TestLateReportsRebuildSessions(t *testing.T) {
	twin := newTwin()
	s := open(t, twin, at("14:00"))
	r, _ := s.Report("shop", []Report{
		{Table: "A01", Plant: "p1", State: Occupied, People: 3, Since: at("12:00").UnixMilli(), Source: "cam-1"},
		{Table: "A01", Plant: "p1", State: Free, Since: at("13:10").UnixMilli(), Source: "cam-1"},
	}, "")
	if r.Accepted != 2 {
		t.Fatal(r)
	}
	// the camera box was offline: what it saw in between arrives afterwards
	r, _ = s.Report("shop", []Report{
		{Table: "A01", Plant: "p1", State: Cleaning, Since: at("12:40").UnixMilli(), Source: "cam-1"},
		{Table: "A01", Plant: "p1", State: Occupied, People: 2, Since: at("12:50").UnixMilli(), Source: "cam-1"},
	}, "")
	if r.Late != 2 || r.Accepted != 0 {
		t.Fatalf("late: %+v", r)
	}
	if twin.tables[0].State != Free {
		t.Errorf("a late report changed the twin: %+v (told %v)", twin.tables[0], twin.told)
	}
	day, err := s.Day("shop", "p1", "A01", at("14:00"))
	if err != nil {
		t.Fatal(err)
	}
	if len(day) != 2 || day[0].Start != at("12:00").UnixMilli() || *day[0].End != at("12:40").UnixMilli() ||
		day[1].Start != at("12:50").UnixMilli() || *day[1].End != at("13:10").UnixMilli() || day[1].People != 2 || day[0].People != 3 {
		t.Fatalf("sessions: %+v", day)
	}
}

func TestPOSEvents(t *testing.T) {
	twin := newTwin()
	s := open(t, twin, at("19:30"))
	r, err := s.Events("shop", []Event{
		{Table: "B01", Plant: "p1", Event: Seated, People: 5, At: at("18:00").UnixMilli(), Source: "pos"},
		{Table: "B01", Plant: "p1", Event: Ordered, At: at("18:05").UnixMilli(), Source: "pos"},
		{Table: "B01", Plant: "p1", Event: Paid, At: at("19:10").UnixMilli(), Source: "pos"},
		{Table: "B01", Plant: "p1", Event: "ate"},
	}, "")
	if err != nil || r.Accepted != 3 || r.Invalid != 1 {
		t.Fatalf("%+v %v", r, err)
	}
	day, _ := s.Day("shop", "p1", "B01", time.Time{})
	if len(day) != 1 || day[0].People != 5 || day[0].Minutes(at("19:30")) != 70 || day[0].Source != "pos" {
		t.Fatalf("session: %+v", day)
	}
	if twin.tables[2].State != Cleaning {
		t.Errorf("after paying the table is to clear: %+v", twin.tables[2])
	}
}

func TestKPIsAndSimFallback(t *testing.T) {
	twin := newTwin()
	s := open(t, twin, at("13:00"))
	s.Report("shop", []Report{{Table: "A01", Plant: "p1", State: Occupied, People: 4, Since: at("12:20").UnixMilli(), Source: "cam-1"}}, "")
	// the twin seats a group at A02 (nothing reports it) and once guessed at A01 (reported: dropped)
	end := at("12:50").UnixMilli()
	twin.sims = []engine.SimSession{
		{Plant: "p1", Table: "A02", Start: at("11:45").UnixMilli(), End: &end, People: 2, Source: Sim},
		{Plant: "p1", Table: "A02", Start: at("12:55").UnixMilli(), People: 2, Source: Sim},
		{Plant: "p1", Table: "A01", Start: at("12:30").UnixMilli(), People: 3, Source: Sim},
	}
	twin.tables[1].State, twin.tables[1].People, twin.tables[1].Since = Occupied, 2, at("12:55").UnixMilli()
	s.drain()
	ov, err := s.Overview("shop")
	if err != nil {
		t.Fatal(err)
	}
	var p1 KPIs
	for _, k := range ov.Plants {
		if k.Plant == "p1" {
			p1 = k
		}
	}
	if p1.Tables != 3 || p1.Occupied != 2 || p1.People != 6 || p1.Seats != 12 || p1.Sessions != 3 || p1.ReportedSessions != 1 {
		t.Fatalf("p1: %+v", p1)
	}
	if p1.Occupancy != 0.667 || p1.SeatUse != 0.5 || p1.Turnover != 1 || p1.AvgMinutes == nil || *p1.AvgMinutes != 65 || p1.Source != "mixed" {
		t.Fatalf("p1 figures: %+v avg %v", p1, p1.AvgMinutes)
	}
	if ov.Total.Tables != 4 || ov.Total.MeasuredShare != 0.25 {
		t.Errorf("total: %+v", ov.Total)
	}
	cur, err := s.Table("shop", "p1", "A01")
	if err != nil || cur.Minutes == nil || *cur.Minutes != 40 || cur.Table.LastSource != "cam-1" {
		t.Fatalf("A01 now: %+v %v", cur, err)
	}
	if _, err := s.Table("shop", "", "A01"); err != ErrAmbiguous {
		t.Errorf("A01 in two plants: %v", err)
	}
	h, err := s.History("shop", at("11:00"), at("14:00"), false)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Bucket{}
	for _, b := range h {
		got[b.Label[11:]] = b
	}
	// A02 11:45–12:50 is 0.25 h in the 11:00 hour; 12:00 holds A02's 50 min, A01's 40, A02's 5
	if got["11:00"].OccupiedHours != 0.25 || got["11:00"].Sessions != 1 || got["12:00"].Sessions != 2 || got["12:00"].OccupiedHours != 1.58 {
		t.Fatalf("history: %+v", h)
	}
}

// A camera box on the shop's LAN publishes on the convention topic; the broker's tags reach the service.
func TestMQTTConvention(t *testing.T) {
	twin := newTwin()
	s := open(t, twin, time.Now())
	hub := tags.NewHub()
	s.Hub = hub
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	time.Sleep(50 * time.Millisecond)
	since := time.Now().Add(-20 * time.Minute).UnixMilli()
	ts, err := mapping.Convention("sf", "sf/shop/p1/B01/table", []byte(fmt.Sprintf(`{"state":"occupied","people":5,"since":%d,"source":"cam-2"}`, since)), "cam-box-1")
	if err != nil || len(ts) != 1 {
		t.Fatalf("convention: %v %v", ts, err)
	}
	for _, x := range ts {
		hub.Publish(x)
	}
	var day []Session
	for deadline := time.Now().Add(5 * time.Second); len(day) == 0 && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
		day, _ = s.Day("shop", "p1", "B01", time.Time{})
	}
	cancel()
	<-done
	if twin.tables[2].People != 5 || len(day) != 1 || day[0].Source != "cam-2" || day[0].Start != since {
		t.Fatalf("over MQTT: %+v %+v", twin.tables[2], day)
	}
}

func TestMQTTTag(t *testing.T) {
	twin := newTwin()
	s := open(t, twin, at("18:00"))
	s.fromTag(tags.Tag{Company: "shop", Plant: "p2", Asset: "A01", Field: Field, Source: "cam-box",
		Value: map[string]any{"state": "occupied", "people": float64(3), "since": "2026-10-17T17:55:00+08:00"}})
	s.fromTag(tags.Tag{Company: "shop", Plant: "p2", Asset: "A01", Field: FieldSession, Source: "pos",
		Value: map[string]any{"event": "paid", "at": float64(at("18:00").UnixMilli())}})
	day, _ := s.Day("shop", "p2", "A01", time.Time{})
	if len(day) != 1 || day[0].Minutes(at("18:00")) != 5 || day[0].Source != "cam-box" || twin.tables[3].State != Cleaning {
		t.Fatalf("mqtt: %+v %+v", day, twin.tables[3])
	}
}
