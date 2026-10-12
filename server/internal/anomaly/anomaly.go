// Package anomaly notices a measured reading that is plausible but unusual for its machine: a
// bearing's vibration creeping up while still under its alarm limit, a temperature sitting high
// for what the machine is doing. The validator (series) keeps out what no machine could read;
// this learns what this machine reads, field by field and state by state (running and idle have
// very different normals), and gives a hint when a reading stays away from that. A hint is not
// an alarm: nothing stops, it goes into the activity log, the twin and the console, and to the
// assistants, for a person to look at.
//
// Only readings from equipment are learned (the hub's Tap); simulated machines send none. A
// flagged reading (Q) is never learned or judged, nor is a late one (a gateway replaying).
package anomaly

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"smartfactory/internal/audit"
	"smartfactory/internal/auth"
	"smartfactory/internal/config"
	"smartfactory/internal/tags"
)

// Kinds of hint.
const (
	// Level: readings stayed K σ away from the normal.
	Level = "level"
	// Drift: the recent average moved off the normal, by less than K σ of a single reading but
	// further than that average usually strays.
	Drift = "drift"
)

// Settings are how the detector judges (config.Anomaly with its defaults).
type Settings struct {
	K       float64
	Sustain time.Duration
	Warmup  time.Duration
	// MinReadings a field must have in a state, besides Warmup, before it is judged in it.
	MinReadings int
	// Span is how many readings the normal averages over; Short how many the recent average does.
	Span, Short int
	Skip        []string
}

func SettingsFrom(c config.Anomaly) Settings {
	s := Settings{K: c.K, Sustain: c.Sustain.Or(time.Minute), Warmup: c.Warmup.Or(24 * time.Hour), MinReadings: 100, Span: c.Span, Short: 20, Skip: c.Skip}
	if s.K <= 0 {
		s.K = 3
	}
	if s.Span <= 0 {
		s.Span = 200000
	}
	return s
}

// skipped are fields that count, place or are set rather than measured: they only grow, jump by
// design or follow a command.
var skipped = []string{"mcount", "count", "cycles", "toolUsed", "toolLife", "faultCode", "x", "z", "heading", "battery", "speed", "health", "stock", "avail", "cap"}

// Anomaly is a hint: a machine's field that is unusual for its state.
type Anomaly struct {
	ID      int64  `json:"id"`
	Company string `json:"company"`
	Plant   string `json:"plant"`
	Asset   string `json:"asset"`
	Field   string `json:"field"`
	// State is the machine's state whose normal it is held against ("" when it reports none).
	State string `json:"state"`
	// Kind: level | drift
	Kind  string     `json:"kind"`
	Since time.Time  `json:"since"`
	Ended *time.Time `json:"ended,omitempty"`
	// Value is the reading now (level) or the recent average (drift); Z how far it is from the
	// normal in standard deviations of a reading; Peak the furthest it went.
	Value float64 `json:"value"`
	Z     float64 `json:"z"`
	Peak  float64 `json:"peak"`
	// Mean ± Sigma is the normal of the field in that state.
	Mean   float64 `json:"mean"`
	Sigma  float64 `json:"sigma"`
	Source string  `json:"source"`
	// Text is the hint in words.
	Text string `json:"text"`
}

// Stat is what a field normally reads in one state: Mean and Var, and Short, the average of the
// last few readings, with ShortVar, how far that average usually strays from Mean.
type Stat struct {
	N               int64
	Mean, Var       float64
	Short, ShortVar float64
	// First and Last reading, epoch milliseconds.
	First, Last int64
	dirty       bool
}

// sigma is the standard deviation, with a floor: a sensor that reads the same value for an hour
// has σ 0, and then a tenth of a degree would be infinitely unusual.
func (s *Stat) sigma() float64 {
	return math.Max(math.Sqrt(s.Var), math.Max(0.01*math.Abs(s.Mean), 0.05))
}

// learn takes x into the mean with weight a and into the spreads with weight b.
func (s *Stat) learn(x, a, b float64) {
	s.N++
	d := x - s.Mean
	s.Mean += a * d
	s.Var = (1 - b) * (s.Var + b*d*d)
	e := s.Short - s.Mean
	s.ShortVar += b * (e*e - s.ShortVar)
	s.dirty = true
}

type mkey struct{ company, plant, asset string }

type machine struct {
	state, source string
	fields        map[string]*field
}

// field is one field of a machine: its normal in each state and how it is judged now.
type field struct {
	states map[string]*Stat
	cur    *Stat
	// the last reading and how far it was, and its normal before it was learned
	value, z float64
	at       time.Time
	before   Stat
	// since when (and for how many readings in a row) it has been beyond K σ, its average
	// beyond K of its own spread, and back within both
	outSince, driftSince, inSince time.Time
	outN, driftN                  int
	hint                          *Anomaly
}

type event struct {
	ended bool
	a     Anomaly
}

// Detector learns the measured machines' normals and keeps their hints.
type Detector struct {
	Set   Settings
	Audit *audit.Log
	Log   *slog.Logger
	// Now is the clock for what is not a reading's own time (tests set it).
	Now func() time.Time

	store    *Store
	skip     map[string]bool
	mu       sync.Mutex
	machines map[mkey]*machine
	nextID   int64
	events   chan event
	dropped  atomic.Uint64
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

// New is a detector keeping its normals and hints in st (nil: nothing kept), loaded from it.
func New(st *Store, set Settings, trail *audit.Log, log *slog.Logger) (*Detector, error) {
	if log == nil {
		log = slog.Default()
	}
	d := &Detector{Set: set, Audit: trail, Log: log, Now: time.Now, store: st, skip: map[string]bool{}, machines: map[mkey]*machine{}, events: make(chan event, 1024)}
	for _, f := range append(slices.Clone(skipped), set.Skip...) {
		d.skip[f] = true
	}
	if st != nil {
		rows, err := st.baselines()
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			s := r.stat
			d.fieldOf(d.machineOf(mkey{r.company, r.plant, r.asset}), r.field).states[r.state] = &s
		}
		if d.nextID, err = st.lastID(); err != nil {
			return nil, err
		}
	}
	return d, nil
}

func (d *Detector) machineOf(k mkey) *machine {
	m := d.machines[k]
	if m == nil {
		m = &machine{fields: map[string]*field{}}
		d.machines[k] = m
	}
	return m
}

func (d *Detector) fieldOf(m *machine, name string) *field {
	f := m.fields[name]
	if f == nil {
		f = &field{states: map[string]*Stat{}}
		m.fields[name] = f
	}
	return f
}

func number(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	}
	return 0, false
}

// Add learns and judges one reading. It is the hub's Tap: it must not block, and does no more
// than a few map lookups and sums (no allocation once a field is known).
func (d *Detector) Add(t tags.Tag) {
	if t.Q&tags.Doubtful != 0 || (!t.At.IsZero() && d.Now().Sub(t.At) > tags.Late) {
		return
	}
	at := t.At
	if at.IsZero() {
		at = d.Now()
	}
	if t.Field == "state" {
		s, ok := t.Value.(string)
		if !ok {
			return
		}
		d.mu.Lock()
		m := d.machineOf(mkey{t.Company, t.Plant, t.Asset})
		if s != m.state {
			d.switchState(t, m, s, at)
		}
		d.mu.Unlock()
		return
	}
	x, ok := number(t.Value)
	if !ok || math.IsNaN(x) || math.IsInf(x, 0) || d.skip[t.Field] {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	m := d.machineOf(mkey{t.Company, t.Plant, t.Asset})
	m.source = t.Source
	f := d.fieldOf(m, t.Field)
	s := f.states[m.state]
	if s == nil {
		s = &Stat{First: at.UnixMilli()}
		f.states[m.state] = s
	}
	d.judge(t, m, f, s, x, at)
}

// switchState puts the machine in state s. Equipment sends a machine's fields together, in name
// order: the power read with the new state came just before it, and went into the old state's
// normal. A field read at the very moment of the change is taken back out of that normal and
// judged again in the new state (a running machine's power learned as idle widens the idle normal
// until nothing stands out).
func (d *Detector) switchState(t tags.Tag, m *machine, s string, at time.Time) {
	m.state = s
	for name, f := range m.fields {
		if f.cur == nil || !f.at.Equal(at) || f.cur.N != f.before.N+1 {
			continue
		}
		*f.cur = f.before
		f.cur.dirty = true
		st := f.states[s]
		if st == nil {
			st = &Stat{First: at.UnixMilli()}
			f.states[s] = st
		}
		d.judge(tags.Tag{Company: t.Company, Plant: t.Plant, Asset: t.Asset, Field: name, Source: m.source}, m, f, st, f.value, at)
	}
}

// judge holds a reading against its field's normal in the machine's state, learns it, and raises
// or ends the field's hint.
//
// The normal is every reading's average and spread until the field is warmed up, then follows
// slowly: an exponentially weighted average over Span readings, the spreads ten times slower (a
// drift the mean half follows would otherwise widen them until nothing stands out), a reading out
// of the ordinary counting a tenth. So what was normal stays the yardstick for days, and something
// new (a cold start never seen) still becomes normal in time.
//
// Two tests against it: a reading further than K σ from Mean (a step, an excursion that lasts),
// and the average of the last Short readings (an EWMA) further than K+1 times how far that average
// usually strays from Mean (a slow drift: the average moves with it, the normal hardly does, and
// noise mostly averages out). How far the average strays is learned too, so a field that wanders
// slowly by nature (a temperature warming up) is not drifting. Either has to hold for Sustain and
// three readings in a row to be a hint (an excursion holds the drift test's count, or the average
// it leaves behind would be one), and both have to be back within for Sustain to end it.
func (d *Detector) judge(t tags.Tag, m *machine, f *field, s *Stat, x float64, at time.Time) {
	set := d.Set
	f.before = *s
	if f.cur != s {
		// another state: its own normal, and the evidence so far was against the other one
		s.Short, f.cur = x, s
		f.outSince, f.driftSince, f.inSince = time.Time{}, time.Time{}, time.Time{}
		f.outN, f.driftN = 0, 0
	}
	ms := at.UnixMilli()
	warm := s.N >= int64(set.MinReadings) && ms-s.First >= set.Warmup.Milliseconds()
	sigma := s.sigma()
	z := (x - s.Mean) / sigma
	lam := 2 / float64(set.Short+1)
	s.Short += lam * (x - s.Short)
	spread := math.Max(math.Sqrt(s.ShortVar), sigma*math.Sqrt(lam/(2-lam)))
	// a run, once begun, goes on while it stays a σ less out: noise near the line does not break
	// it. The average's own noise is slow (each value carries the last ones): its line is one
	// further out, or it would cross it every few hundred readings.
	hold := func(v, k float64, n int) bool { return warm && (math.Abs(v) > k || (n > 0 && math.Abs(v) > k-1)) }
	level := hold(z, set.K, f.outN)
	drift := hold((s.Short-s.Mean)/spread, set.K+1, f.driftN)
	a := 1 / float64(set.Span)
	switch {
	case !warm:
		a = 1 / float64(s.N+1)
		s.learn(x, a, a)
	case level || drift:
		s.learn(x, a/10, a/100)
	default:
		s.learn(x, a, a/10)
	}
	s.Last = ms
	f.value, f.z, f.at = x, z, at

	if level {
		if f.outN == 0 {
			f.outSince = at
		}
		f.outN++
	} else {
		f.outN = 0
	}
	if drift && !level {
		if f.driftN == 0 {
			f.driftSince = at
		}
		f.driftN++
	} else if !level {
		f.driftN = 0
	}
	// back within: no run of three readings out (a lone reading out does not count)
	if f.outN >= 3 || f.driftN >= 3 || !warm {
		f.inSince = time.Time{}
	} else if f.inSince.IsZero() {
		f.inSince = at
	}
	sustained := func(n int, since time.Time) bool { return n >= 3 && at.Sub(since) >= set.Sustain }
	kind := ""
	switch {
	case sustained(f.outN, f.outSince):
		kind = Level
	case sustained(f.driftN, f.driftSince):
		kind = Drift
	}
	if h := f.hint; h != nil {
		h.Value, h.Z = x, z
		if h.Kind == Drift && kind != Level {
			h.Value, h.Z = s.Short, (s.Short-s.Mean)/sigma
		}
		if math.Abs(h.Z) > math.Abs(h.Peak) {
			h.Peak = h.Z
		}
		if kind == Level && h.Kind == Drift {
			// the drift went on to readings K σ out
			h.Kind = Level
			h.Text = Describe(*h)
		}
		if !f.inSince.IsZero() && at.Sub(f.inSince) >= set.Sustain {
			end := at
			h.Ended = &end
			f.hint = nil
			d.emit(event{ended: true, a: *h})
		}
		return
	}
	if kind == "" {
		return
	}
	d.nextID++
	h := &Anomaly{ID: d.nextID, Company: t.Company, Plant: t.Plant, Asset: t.Asset, Field: t.Field, State: m.state, Kind: kind,
		Since: f.outSince, Value: x, Z: z, Mean: s.Mean, Sigma: sigma, Source: t.Source}
	if kind == Drift {
		h.Since, h.Value, h.Z = f.driftSince, s.Short, (s.Short-s.Mean)/sigma
	}
	h.Peak = h.Z
	h.Text = Describe(*h)
	f.hint = h
	d.emit(event{a: *h})
}

func (d *Detector) emit(e event) {
	select {
	case d.events <- e:
	default:
		d.dropped.Add(1)
	}
}

var fieldLabels = map[string]string{"temp": "温度", "power": "功率", "vib": "振动", "lastMeasure": "实测值", "measure": "实测值"}
var stateLabels = map[string]string{"running": "运行", "idle": "待料", "blocked": "阻塞", "fault": "故障", "stopped": "停机", "maint": "维护", "changeover": "换模"}

// FieldLabel is how a field reads: 温度, 功率 … or the field itself.
func FieldLabel(f string) string {
	if l, ok := fieldLabels[f]; ok {
		return l
	}
	return f
}

// StateLabel is how a state reads; a machine that reports none is 不分状态.
func StateLabel(s string) string {
	if s == "" {
		return "不分状态"
	}
	if l, ok := stateLabels[s]; ok {
		return l
	}
	return s
}

func num(x float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.3f", x), "0"), ".")
}

// Describe is a hint in words: "HP-101 振动偏高：现在 5.2，运行时平常 4.0±0.2（最远 +6.0σ）".
func Describe(a Anomaly) string {
	dir := "偏高"
	if a.Peak < 0 {
		dir = "偏低"
	}
	what := "现在"
	if a.Kind == Drift {
		dir, what = "缓慢"+dir, "近期平均"
	}
	return fmt.Sprintf("%s %s%s：%s %s，%s时平常 %s±%s（最远 %+.1fσ）", a.Asset, FieldLabel(a.Field), dir, what, num(a.Value), StateLabel(a.State), num(a.Mean), num(a.Sigma), a.Peak)
}

// Current are the company's hints now, the most unusual first.
func (d *Detector) Current(company string) []Anomaly {
	d.mu.Lock()
	out := []Anomaly{}
	for k, m := range d.machines {
		if k.company != company {
			continue
		}
		for _, f := range m.fields {
			if f.hint != nil {
				out = append(out, *f.hint)
			}
		}
	}
	d.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return math.Abs(out[i].Peak) > math.Abs(out[j].Peak) })
	return out
}

// Baseline is a field's normal in one state.
type Baseline struct {
	State string  `json:"state"`
	N     int64   `json:"n"`
	Mean  float64 `json:"mean"`
	Sigma float64 `json:"sigma"`
	// Warm: learned long enough to be judged; until then Progress (0–1) says how far along.
	Warm     bool    `json:"warm"`
	Progress float64 `json:"progress"`
}

// Field is one field of a machine as it is judged now.
type Field struct {
	Field string    `json:"field"`
	Value float64   `json:"value"`
	At    time.Time `json:"at"`
	// Z is the last reading's distance from the normal of the machine's state now, in σ.
	Z float64 `json:"z"`
	// Now is the normal of the state the machine is in (absent until a reading in it), States
	// every state's.
	Now     *Baseline  `json:"now,omitempty"`
	States  []Baseline `json:"states"`
	Anomaly *Anomaly   `json:"anomaly,omitempty"`
}

// Machine is a measured machine's fields and their normals.
type Machine struct {
	Plant  string  `json:"plant"`
	Asset  string  `json:"asset"`
	State  string  `json:"state"`
	Source string  `json:"source"`
	Fields []Field `json:"fields"`
}

func (d *Detector) baseline(state string, s *Stat) Baseline {
	b := Baseline{State: state, N: s.N, Mean: s.Mean, Sigma: s.sigma()}
	span := s.Last - s.First
	b.Warm = s.N >= int64(d.Set.MinReadings) && span >= d.Set.Warmup.Milliseconds()
	b.Progress = 1
	if !b.Warm {
		b.Progress = math.Min(float64(s.N)/float64(max(d.Set.MinReadings, 1)), float64(span)/math.Max(float64(d.Set.Warmup.Milliseconds()), 1))
		b.Progress = math.Round(math.Min(b.Progress, 1)*100) / 100
	}
	return b
}

func (d *Detector) machineView(k mkey, m *machine) Machine {
	out := Machine{Plant: k.plant, Asset: k.asset, State: m.state, Source: m.source, Fields: []Field{}}
	for name, f := range m.fields {
		x := Field{Field: name, Value: f.value, At: f.at, Z: f.z, States: []Baseline{}}
		for st, s := range f.states {
			b := d.baseline(st, s)
			x.States = append(x.States, b)
			if st == m.state && s.N > 0 {
				x.Now = &b
			}
		}
		sort.Slice(x.States, func(i, j int) bool { return x.States[i].N > x.States[j].N })
		if f.hint != nil {
			h := *f.hint
			x.Anomaly = &h
		}
		out.Fields = append(out.Fields, x)
	}
	sort.Slice(out.Fields, func(i, j int) bool { return fieldOrder(out.Fields[i].Field) < fieldOrder(out.Fields[j].Field) })
	return out
}

func fieldOrder(f string) string {
	switch f {
	case "temp":
		return "0"
	case "power":
		return "1"
	case "vib":
		return "2"
	}
	return "3" + f
}

// Machine is one machine's fields; false when nothing of it was ever learned.
func (d *Detector) Machine(company, plant, asset string) (Machine, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	k := mkey{company, plant, asset}
	m := d.machines[k]
	if m == nil || len(m.fields) == 0 {
		return Machine{}, false
	}
	return d.machineView(k, m), true
}

// Machines are the company's machines with something learned, by plant and asset.
func (d *Detector) Machines(company string) []Machine {
	d.mu.Lock()
	out := []Machine{}
	for k, m := range d.machines {
		if k.company == company && len(m.fields) > 0 {
			out = append(out, d.machineView(k, m))
		}
	}
	d.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Plant+"/"+out[i].Asset < out[j].Plant+"/"+out[j].Asset })
	return out
}

// Report is a company's hints now, those of the last 30 days, its measured machines' normals and
// how they are judged.
type Report struct {
	Current  []Anomaly `json:"current"`
	Recent   []Anomaly `json:"recent"`
	Machines []Machine `json:"machines"`
	Settings struct {
		K float64 `json:"k"`
		// Sustain and Warmup in seconds; Span in readings.
		Sustain float64 `json:"sustain"`
		Warmup  float64 `json:"warmup"`
		Span    int     `json:"span"`
	} `json:"settings"`
}

func (d *Detector) Report(company string) (*Report, error) {
	recent, err := d.Recent(company, 100)
	if err != nil {
		return nil, err
	}
	r := &Report{Current: d.Current(company), Recent: recent, Machines: d.Machines(company)}
	r.Settings.K, r.Settings.Sustain, r.Settings.Warmup, r.Settings.Span = d.Set.K, d.Set.Sustain.Seconds(), d.Set.Warmup.Seconds(), d.Set.Span
	return r, nil
}

// Recent are the company's hints of the last 30 days, newest first (ended and current).
func (d *Detector) Recent(company string, limit int) ([]Anomaly, error) {
	if d.store == nil {
		return []Anomaly{}, nil
	}
	return d.store.recent(company, d.Now().Add(-30*24*time.Hour), limit)
}

// Reset forgets what was learned of a machine (after maintenance its normal is another) and
// ends its hints; false when nothing was learned of it.
func (d *Detector) Reset(company, plant, asset string) (bool, error) {
	k := mkey{company, plant, asset}
	d.mu.Lock()
	m := d.machines[k]
	var ended []Anomaly
	if m != nil {
		now := d.Now()
		for _, f := range m.fields {
			if f.hint != nil {
				f.hint.Ended = &now
				ended = append(ended, *f.hint)
			}
		}
		delete(d.machines, k)
	}
	d.mu.Unlock()
	if m == nil {
		return false, nil
	}
	if d.store != nil {
		if err := d.store.forget(company, plant, asset); err != nil {
			return true, err
		}
		for _, a := range ended {
			if err := d.store.end(a); err != nil {
				return true, err
			}
		}
	}
	return true, nil
}

// Dropped is how many hints did not reach the log because it fell behind.
func (d *Detector) Dropped() uint64 { return d.dropped.Load() }

// Start keeps hints in the store and the activity log as they come, and the normals every few
// minutes, until ctx ends or Close.
func (d *Detector) Start(ctx context.Context) {
	ctx, d.cancel = context.WithCancel(ctx)
	d.wg.Add(1)
	go d.run(ctx)
}

func (d *Detector) run(ctx context.Context) {
	defer d.wg.Done()
	save := time.NewTicker(5 * time.Minute)
	defer save.Stop()
	for {
		select {
		case <-ctx.Done():
			d.Flush()
			return
		case e := <-d.events:
			d.record(e)
		case <-save.C:
			d.Save()
		}
	}
}

// Flush records the hints waiting and saves the normals.
func (d *Detector) Flush() {
	for {
		select {
		case e := <-d.events:
			d.record(e)
			continue
		default:
		}
		break
	}
	d.Save()
}

// Close stops Start's loop once it has saved, then closes the store.
func (d *Detector) Close() error {
	if d.cancel != nil {
		d.cancel()
		d.wg.Wait()
	} else {
		d.Flush()
	}
	if d.store != nil {
		return d.store.Close()
	}
	return nil
}

// who the activity log says a hint is from
var detector = auth.Who{Name: "异常检测"}

func (d *Detector) record(e event) {
	a := e.a
	target := a.Plant + "/" + a.Asset
	if e.ended {
		d.Audit.RecordWith(detector, "anomaly", a.Company, "anomaly.end", target, FieldLabel(a.Field)+"恢复正常", fmt.Sprintf("持续 %s，最远 %+.1fσ", a.Ended.Sub(a.Since).Round(time.Second), a.Peak), nil)
	} else {
		d.Audit.RecordWith(detector, "anomaly", a.Company, "anomaly", target, a.Text, fmt.Sprintf("来源 %s；%s时的平常值学自这台设备的读数，只是提示，设备照常运行", a.Source, StateLabel(a.State)), nil)
	}
	if d.store == nil {
		return
	}
	var err error
	if e.ended {
		err = d.store.end(a)
	} else {
		err = d.store.add(a)
	}
	if err != nil {
		d.Log.Error("anomaly: keep hint", "err", err)
	}
}

// Save writes the normals that changed since the last save.
func (d *Detector) Save() {
	if d.store == nil {
		return
	}
	var rows []row
	d.mu.Lock()
	for k, m := range d.machines {
		for name, f := range m.fields {
			for st, s := range f.states {
				if s.dirty {
					s.dirty = false
					rows = append(rows, row{k.company, k.plant, k.asset, name, st, *s})
				}
			}
		}
	}
	d.mu.Unlock()
	if err := d.store.save(rows); err != nil {
		d.Log.Error("anomaly: save normals", "err", err, "rows", len(rows))
		// keep them for the next save
		d.mu.Lock()
		for _, r := range rows {
			if m := d.machines[mkey{r.company, r.plant, r.asset}]; m != nil {
				if f := m.fields[r.field]; f != nil {
					if s := f.states[r.state]; s != nil {
						s.dirty = true
					}
				}
			}
		}
		d.mu.Unlock()
	}
}
