package checks

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"smartfactory/internal/config"
	"smartfactory/internal/engine"
	"smartfactory/internal/series"
	"smartfactory/internal/tags"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func open(t *testing.T, eng *engine.Engine, hub *tags.Hub) *Service {
	t.Helper()
	s, err := Open(t.TempDir(), eng, hub, quiet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// Forms that would be filled in wrong, or be read wrong, are refused with every reason; the
// templates pass, and a choice's abnormal answers default to 异常 and 不合格.
func TestFormValidation(t *testing.T) {
	s := open(t, nil, nil)
	for _, f := range Templates() {
		if p := s.CheckForm("c", &f); len(p) > 0 {
			t.Errorf("template %s: %v", f.Name, p)
		}
	}
	f := Form{Name: "巡检", Fields: []Field{{Key: "a", Label: "结论", Kind: Choice, Options: []string{"合格", "不合格", "合格"}}}}
	if p := s.CheckForm("c", &f); len(p) > 0 {
		t.Fatal(p)
	}
	if !slices.Equal(f.Fields[0].Options, []string{"合格", "不合格"}) || !slices.Equal(f.Fields[0].Bad, []string{"不合格"}) || f.Kind != "other" {
		t.Errorf("filled in: %+v", f)
	}

	bad := Form{Name: "", Kind: "audit", Lines: []string{"l1"},
		Fields: []Field{
			{Key: "1x", Label: "数", Kind: Number},
			{Key: "temp", Label: "温度", Kind: Number},
			{Key: "h", Label: "硬度", Kind: Number, Lo: f64(300), Hi: f64(200)},
			{Key: "h", Label: "重复", Kind: Text},
			{Key: "c", Label: "选", Kind: Choice, Options: []string{"好"}, Bad: []string{"坏"}},
			{Key: "m", Label: "尺寸", Kind: Number, Twin: "pressure"},
			{Key: "x", Label: "怪", Kind: "slider"},
		},
		Schedule: Schedule{Every: "hours", Hours: 0, At: []string{"8:00"}},
	}
	p := strings.Join(s.CheckForm("c", &bad), "\n")
	for _, want := range []string{"名称", "类型要是 patrol", "要先选厂", "标识要以字母开头", "temp 是设备上报用的字段名", "下限比上限大",
		"和前面的重复了", "选项要 2～12 个", "「坏」不在选项里", "measure、temp、power 或 vib", "类型要是 number", "1～24 小时", `"8:00"`} {
		if !strings.Contains(p, want) {
			t.Errorf("no problem about %q in:\n%s", want, p)
		}
	}
	if _, err := s.SaveForm("c", bad, "厂长"); err == nil {
		t.Error("a bad form was saved")
	}
	saved, err := s.SaveForm("c", Templates()[0], "厂长")
	if err != nil || !strings.HasPrefix(saved.ID, "F-") || saved.By != "厂长" {
		t.Fatal(saved, err)
	}
	saved.Name = "早班点检"
	again, err := s.SaveForm("c", *saved, "厂长")
	if err != nil || again.ID != saved.ID || !again.Created.Equal(saved.Created) {
		t.Fatal(again, err)
	}
	if fs, _ := s.Forms("c"); len(fs) != 1 || fs[0].Name != "早班点检" {
		t.Errorf("forms: %+v", fs)
	}
	if _, err := s.DeleteForm("c", saved.ID); err != nil {
		t.Fatal(err)
	}
	if fs, _ := s.Forms("c"); len(fs) != 0 {
		t.Errorf("a deleted form is listed: %+v", fs)
	}
	if f, err := s.Form("c", saved.ID); err != nil || f.Name != "早班点检" {
		t.Errorf("a deleted form can no longer be read: %v", err)
	}
}

// A round each shift: done in the shift it was filled in, due until then, overdue once a shift
// went by without it or its time within the shift is up — across midnight for the night shift.
func TestRoundsAcrossShifts(t *testing.T) {
	s := open(t, nil, nil)
	loc := s.Loc
	at := func(day, hh, mm int) time.Time { return time.Date(2026, 10, day, hh, mm, 0, 0, loc) }
	ptr := func(t time.Time) *time.Time { return &t }
	shift := Form{ID: "F-1", Name: "点检", Schedule: Schedule{Every: "shift", At: []string{"20:00", "08:00"}}, Created: at(1, 0, 0)}
	cases := []struct {
		name  string
		f     Form
		now   time.Time
		last  *time.Time
		want  string
		start time.Time
		due   time.Time
		since time.Time
	}{
		{"night shift, the day shift's round was done", shift, at(12, 21, 0), ptr(at(12, 19, 0)), "due", at(12, 20, 0), at(13, 8, 0), time.Time{}},
		{"night shift, the day shift's round was missed", shift, at(12, 21, 0), ptr(at(12, 7, 0)), "overdue", at(12, 20, 0), at(13, 8, 0), at(12, 20, 0)},
		{"after midnight, done in this night shift", shift, at(13, 2, 0), ptr(at(12, 22, 0)), "done", at(12, 20, 0), at(13, 8, 0), time.Time{}},
		{"after midnight, not yet", shift, at(13, 2, 0), ptr(at(12, 15, 0)), "due", at(12, 20, 0), at(13, 8, 0), time.Time{}},
		{"never filled in", shift, at(13, 2, 0), nil, "overdue", at(12, 20, 0), at(13, 8, 0), at(12, 20, 0)},
		{"within two hours of the shift's start, and late", Form{ID: "F-2", Schedule: Schedule{Every: "shift", Within: 120}, Created: at(1, 0, 0)},
			at(12, 22, 30), ptr(at(12, 15, 0)), "overdue", at(12, 20, 0), at(12, 22, 0), at(12, 22, 0)},
		{"within two hours, in time", Form{ID: "F-2", Schedule: Schedule{Every: "shift", Within: 120}, Created: at(1, 0, 0)},
			at(12, 21, 30), ptr(at(12, 15, 0)), "due", at(12, 20, 0), at(12, 22, 0), time.Time{}},
		{"a form set up this shift is not behind yet", Form{ID: "F-3", Schedule: Schedule{Every: "shift"}, Created: at(12, 21, 0)},
			at(12, 21, 30), nil, "due", at(12, 20, 0), at(13, 8, 0), time.Time{}},
		{"every 4 hours from 08:00, before the day's first", Form{ID: "F-4", Schedule: Schedule{Every: "hours", Hours: 4, At: []string{"08:00"}}, Created: at(1, 0, 0)},
			at(12, 7, 0), ptr(at(12, 1, 0)), "due", at(12, 4, 0), at(12, 8, 0), time.Time{}},
		{"daily at 07:30, missed yesterday", Form{ID: "F-5", Schedule: Schedule{Every: "day", At: []string{"07:30"}}, Created: at(1, 0, 0)},
			at(12, 9, 0), ptr(at(10, 9, 0)), "overdue", at(12, 7, 30), at(13, 7, 30), at(12, 7, 30)},
		{"no schedule", Form{ID: "F-6"}, at(12, 9, 0), nil, "open", time.Time{}, time.Time{}, time.Time{}},
	}
	for _, c := range cases {
		s.Now = func() time.Time { return c.now }
		r := s.round(&c.f, c.last)
		if r.Status != c.want {
			t.Errorf("%s: %s, want %s", c.name, r.Status, c.want)
		}
		get := func(p *time.Time) time.Time {
			if p == nil {
				return time.Time{}
			}
			return *p
		}
		if !get(r.Start).Equal(c.start) || !get(r.Due).Equal(c.due) || !get(r.Since).Equal(c.since) {
			t.Errorf("%s: start %v due %v since %v, want %v %v %v", c.name, get(r.Start), get(r.Due), get(r.Since), c.start, c.due, c.since)
		}
	}
}

// Filling a form in on a machine of a running twin: its numbers are readings with source manual
// at the time they were taken, in the series store; a measure reaches the machine's quality chart;
// a value out of spec or an abnormal answer raises a quality alarm and a later good one clears it;
// a phone that was offline sends a round hours later — stored with its own time, kept from the
// twin and from the alarms, kept once however often it is sent; the round counts as done.
func TestSubmit(t *testing.T) {
	if _, err := os.Stat("../engine/js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	eng, err := engine.New(quiet)
	if err != nil {
		t.Fatal(err)
	}
	eng.Dir = t.TempDir()
	eng.Single()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); <-eng.Done })
	go eng.Run(ctx)
	var cfg map[string]any
	json.Unmarshal([]byte(eng.BuiltinTemplate("huaduan")), &cfg)
	cfg["id"], cfg["name"], cfg["short"] = "jianghe", "江河锻造", "江河"
	b, _ := json.Marshal(cfg)
	if r := eng.CreateCompany(b); !r.OK {
		t.Fatal(r.Errors)
	}

	// readings go where equipment's go: checked, stored, and the live ones into the twin
	store, err := series.Open(t.TempDir(), series.RetentionFrom(config.Series{}), quiet)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	hub := tags.NewHub()
	validator := &series.Validator{Machines: eng.Machines}
	hub.Check = validator.Check
	var mu sync.Mutex
	var tapped, live []tags.Tag
	hub.Tap = func(tg tags.Tag) {
		mu.Lock()
		tapped = append(tapped, tg)
		mu.Unlock()
		store.Add(tg)
	}
	sub := hub.Subscribe()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case e := <-sub.C:
				if e.Type == "tag" {
					mu.Lock()
					live = append(live, *e.Tag)
					mu.Unlock()
					eng.Ingest(e.Tag.Company, e.Tag.Plant, e.Tag.Asset, e.Tag.Field, e.Tag.Value, e.Tag.Source)
				}
			}
		}
	}()

	s := open(t, eng, hub)
	// a forging press with a quality measure
	var plant, asset string
	var spec *engine.Spec
	for _, p := range eng.Stations("jianghe") {
		for _, l := range p.Lines {
			for _, st := range l.Stations {
				if spec == nil && st.Measure != nil && st.Measure.Lo != nil && st.Measure.Hi != nil {
					plant, asset, spec = p.ID, st.ID, st.Measure
				}
			}
		}
	}
	if spec == nil {
		t.Fatal("no station with a measure in the template")
	}
	first := Templates()[1]
	first.Plant, first.Assets = plant, []string{asset}
	form, err := s.SaveForm("jianghe", first, "厂长")
	if err != nil {
		t.Fatal(err)
	}
	if m, ok, _ := s.Machine("jianghe", plant, asset); !ok || len(m.Rounds) != 1 || m.Rounds[0].Status == "done" {
		t.Fatalf("the press's round before anyone filled it in: %+v", m)
	}

	zhang := Author{Name: "张工", Via: "page"}
	alarms := func() []string {
		st, _ := eng.AssetState("jianghe", plant, asset)
		return slices.DeleteFunc(st.Alarms, func(a string) bool { return !strings.HasPrefix(a, form.Name) })
	}
	waitAlarm := func(want bool) []string {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(20 * time.Millisecond) {
			if a := alarms(); (len(a) > 0) == want {
				return a
			}
			if time.Now().After(deadline) {
				t.Fatalf("alarms %v, want some: %v", alarms(), want)
			}
		}
	}

	// a first unit out of spec, and judged so: by the machine's own spec
	over := *spec.Hi + (*spec.Hi-*spec.Lo)/4
	taken := time.Now().Add(-5 * time.Second).Truncate(time.Millisecond)
	got, err := s.Submit("jianghe", Input{ID: "phone-1", Form: form.ID, Plant: plant, Asset: asset, Taken: &taken,
		Values: map[string]any{"size": over, "look": "合格", "verdict": "不合格"}}, zhang)
	if err != nil {
		t.Fatal(err)
	}
	if got.Bad != 2 || got.Late || !got.Taken.Equal(taken) || got.By != "张工" {
		t.Errorf("submission: %+v", got)
	}
	size := got.Values[0]
	if size.Field != "measure" || !size.Bad || size.Hi == nil || *size.Hi != *spec.Hi || size.Unit != spec.Unit {
		t.Errorf("size judged %+v by %+v", size, spec)
	}
	mu.Lock()
	var reading *tags.Tag
	for i := range tapped {
		if tapped[i].Asset == asset && tapped[i].Field == "measure" {
			reading = &tapped[i]
		}
	}
	mu.Unlock()
	if reading == nil || reading.Source != Source || !reading.At.Equal(taken) || reading.Value != over || reading.Q != 0 {
		t.Fatalf("the reading: %+v", reading)
	}
	if a := waitAlarm(true); len(a) != 2 || !strings.Contains(a[0]+a[1], "超上限") || !strings.Contains(a[0]+a[1], "结论：不合格") || !strings.Contains(a[0], "张工") {
		t.Errorf("alarms: %v", a)
	}
	// the twin took the unit on its quality chart
	var hist struct{ Meas []float64 }
	json.Unmarshal([]byte(eng.Hist("jianghe", plant, asset)), &hist)
	if len(hist.Meas) == 0 || hist.Meas[len(hist.Meas)-1] != over {
		t.Errorf("the quality chart's last unit: %v, want %v", hist.Meas[max(0, len(hist.Meas)-3):], over)
	}
	if st, _ := eng.AssetState("jianghe", plant, asset); st.Live != "" {
		t.Errorf("a person's reading made the machine count as reporting: %q", st.Live)
	}

	// the same round sent again (the phone never heard back): kept once
	again, err := s.Submit("jianghe", Input{ID: "phone-1", Form: form.ID, Plant: plant, Asset: asset, Taken: &taken, Values: map[string]any{"size": over}}, zhang)
	if err != nil || !again.Duplicate || again.Bad != 2 {
		t.Fatalf("resent: %+v %v", again, err)
	}

	// a good unit a minute later clears the alarms
	good := time.Now().Truncate(time.Millisecond)
	if _, err := s.Submit("jianghe", Input{Form: form.ID, Plant: plant, Asset: asset, Taken: &good,
		Values: map[string]any{"size": spec.Target, "look": "合格", "verdict": "合格"}}, zhang); err != nil {
		t.Fatal(err)
	}
	waitAlarm(false)

	// a phone that was offline for two hours sends a bad round from then: stored at its time,
	// kept from the twin (it is past it) and from the alarms (a later round is in)
	old := time.Now().Add(-2 * time.Hour).Truncate(time.Millisecond)
	late, err := s.Submit("jianghe", Input{ID: "phone-2", Form: form.ID, Plant: plant, Asset: asset, Taken: &old,
		Values: map[string]any{"size": over, "look": "不合格", "verdict": "不合格"}}, Author{Name: "李师傅", Via: "page"})
	if err != nil {
		t.Fatal(err)
	}
	if !late.Late || late.Bad != 3 {
		t.Errorf("late: %+v", late)
	}
	time.Sleep(200 * time.Millisecond)
	if a := alarms(); len(a) > 0 {
		t.Errorf("an old round raised alarms over a newer good one: %v", a)
	}
	mu.Lock()
	for _, tg := range live {
		if tg.At.Equal(old) {
			t.Errorf("the late reading reached the twin: %+v", tg)
		}
	}
	mu.Unlock()
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}
	ans, err := store.Read(series.Query{Company: "jianghe", Plant: plant, Asset: asset, Fields: []string{"measure"}, From: old.Add(-time.Minute), To: time.Now().Add(time.Second), Step: series.Raw})
	if err != nil {
		t.Fatal(err)
	}
	var times []int64
	for _, p := range ans.Series[0].Points {
		times = append(times, int64(p[0].(int64)))
	}
	if !slices.Contains(times, old.UnixMilli()) || !slices.Contains(times, taken.UnixMilli()) || !slices.Contains(times, good.UnixMilli()) {
		t.Errorf("stored readings at %v, want %v, %v and %v", times, old.UnixMilli(), taken.UnixMilli(), good.UnixMilli())
	}

	// a slip of the finger: kept, flagged, and no alarm for it
	typo := time.Now().Truncate(time.Millisecond)
	slip, err := s.Submit("jianghe", Input{Form: form.ID, Plant: plant, Asset: asset, Taken: &typo, Values: map[string]any{"size": spec.Target * 1000, "look": "合格", "verdict": "合格"}}, zhang)
	if err != nil {
		t.Fatal(err)
	}
	if slip.Values[0].Flag == "" || !slip.Values[0].Bad {
		t.Errorf("an implausible value: %+v", slip.Values[0])
	}
	time.Sleep(200 * time.Millisecond)
	if a := alarms(); len(a) > 0 {
		t.Errorf("an implausible value raised an alarm: %v", a)
	}

	// what is wrong with a submission, all at once
	_, err = s.Submit("jianghe", Input{Form: form.ID, Plant: plant, Asset: asset, Values: map[string]any{"size": "很大", "look": "还行"}}, zhang)
	if err == nil || !strings.Contains(err.Error(), "关键尺寸 要填数字") || !strings.Contains(err.Error(), "外观 要从") || !strings.Contains(err.Error(), "结论 要填") {
		t.Errorf("a bad submission: %v", err)
	}
	other := ""
	for _, p := range eng.Stations("jianghe") {
		for _, l := range p.Lines {
			for _, st := range l.Stations {
				if st.ID != asset && other == "" && p.ID == plant {
					other = st.ID
				}
			}
		}
	}
	if _, err := s.Submit("jianghe", Input{Form: form.ID, Plant: plant, Asset: other, Values: map[string]any{"verdict": "合格", "size": 1, "look": "合格"}}, zhang); err == nil || !strings.Contains(err.Error(), "不用于") {
		t.Errorf("a form on a machine it is not for: %v", err)
	}

	// the round is done; the history lists the latest first
	m, _, _ := s.Machine("jianghe", plant, asset)
	if m.Rounds[0].Status != "done" || m.Rounds[0].LastBy != "张工" {
		t.Errorf("round: %+v", m.Rounds[0])
	}
	list, _ := s.Submissions("jianghe", Filter{Asset: asset})
	if len(list) != 4 || list[0].ID != slip.ID || list[3].ID != "phone-2" {
		t.Errorf("history: %d", len(list))
	}
	if bad, _ := s.Submissions("jianghe", Filter{Bad: true, From: old.Add(time.Minute)}); len(bad) != 2 {
		t.Errorf("bad ones since: %d", len(bad))
	}
	if latest, _ := s.Latest("jianghe", plant, asset); len(latest) != 1 || latest[0].ID != slip.ID {
		t.Errorf("latest: %+v", latest)
	}
	var csv strings.Builder
	s.WriteCSV(&csv, list)
	if lines := strings.Count(csv.String(), "\n"); lines != 1+4*3 || !strings.Contains(csv.String(), "李师傅") {
		t.Errorf("csv: %d lines\n%s", lines, csv.String())
	}
}
