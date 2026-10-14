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
	// Drift: the average of the last minutes stayed DriftK σ off the normal for DriftSustain:
	// a real shift, short of K σ.
	Drift = "drift"
)

// Settings are how the detector judges (config.Anomaly with its defaults).
type Settings struct {
	// K σ held for Sustain is a level hint; DriftK σ of the average over DriftWindow, held for
	// DriftSustain, a drift hint.
	K, DriftK             float64
	Sustain, DriftSustain time.Duration
	DriftWindow           time.Duration
	// Clear is how long a field has to be back within before its hint ends; Cooldown how long
	// after that the same kind of hint waits, unless it is much worse.
	Clear, Cooldown time.Duration
	Warmup          time.Duration
	// MinReadings a field must have in a state, besides Warmup, before it is judged in it.
	MinReadings int
	// Span is how many readings the normal averages over.
	Span int
	// Gap is how long a machine can be silent and go on being judged as it was; SettleMax how long
	// after a gap a field out of range is left to come back before it is judged again.
	Gap, SettleMax time.Duration
	// MaxOpen hints open, or MaxPerHour begun in the last hour, and a company's new hints wait:
	// its normals are off, not its machines.
	MaxOpen, MaxPerHour int
	Skip                []string
}

func SettingsFrom(c config.Anomaly) Settings {
	s := Settings{K: c.K, DriftK: c.DriftK, Sustain: c.Sustain.Or(time.Minute), DriftSustain: c.DriftSustain.Or(30 * time.Minute), DriftWindow: 10 * time.Minute,
		Clear: c.Clear.Or(5 * time.Minute), Cooldown: c.Cooldown.Or(30 * time.Minute), Warmup: c.Warmup.Or(24 * time.Hour), MinReadings: 100, Span: c.Span,
		Gap: time.Minute, SettleMax: 2 * time.Hour, MaxOpen: c.MaxOpen, MaxPerHour: c.MaxPerHour, Skip: c.Skip}
	if s.K <= 0 {
		s.K = 3
	}
	if s.DriftK <= 0 {
		s.DriftK = 2
	}
	if s.Span <= 0 {
		s.Span = 200000
	}
	if s.MaxOpen <= 0 {
		s.MaxOpen = 15
	}
	if s.MaxPerHour <= 0 {
		s.MaxPerHour = 20
	}
	return s
}

// skipped are fields that count, place, are set or are stock rather than a machine's condition:
// they only grow, jump by design or follow a command. A measured part (measure, lastMeasure)
// drifts with the tool by design, and the twin's SPC watches it already. A name ending in "." or
// "*" is a prefix: inv.punch, cap.punch are a warehouse's stock and capacity.
var skipped = []string{"mcount", "count", "cycles", "toolUsed", "toolLife", "faultCode", "x", "z", "heading", "battery", "speed", "health", "stock",
	"avail", "cap", "task", "measure", "lastMeasure", "inv.", "cap."}

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
// last DriftWindow (weighted by time, so a source reading once a second and one reading every five
// average over the same minutes).
type Stat struct {
	N         int64
	Mean, Var float64
	Short     float64
	// Mid is the average of the last minute (reading noise mostly gone), MidVar how far it strays
	// from Mean, DVar how fast it moves: the mean of its (change per second)². A temperature moves
	// slowly and wanders far (a press warming after a stop takes most of an hour), a vibration
	// jumps about but stays put: √(MidVar/DVar) is how long the field takes to cross its spread.
	Mid, MidVar, DVar float64
	// First and Last reading, epoch milliseconds.
	First, Last int64
	dirty       bool
}

// sigma is the standard deviation, with a floor: a sensor that reads the same value for an hour
// has σ 0, and then a tenth of a degree would be infinitely unusual.
func (s *Stat) sigma() float64 {
	return math.Max(math.Sqrt(s.Var), math.Max(0.01*math.Abs(s.Mean), 0.05))
}

// pace is how long the field takes to wander across its spread, from its learned speed.
func (s *Stat) pace() time.Duration {
	if s.DVar <= 0 {
		return 0
	}
	return time.Duration(math.Sqrt(s.MidVar/s.DVar) * float64(time.Second))
}

// learn takes x into the mean with weight a and into the spreads with weight b.
func (s *Stat) learn(x, a, b float64) {
	s.N++
	d := x - s.Mean
	s.Mean += a * d
	s.Var = (1 - b) * (s.Var + b*d*d)
	s.dirty = true
}

type mkey struct{ company, plant, asset string }

type machine struct {
	state, source string
	// stated: it reports its state (now, or before a restart); until the state comes again its
	// readings cannot be told apart and are let pass
	stated bool
	// since: when it changed to its state
	since time.Time
	// seen: its last reading; epoch counts its gaps (silent past Gap, back from another source, or
	// first heard since the server started), after each of which its fields settle again
	seen   time.Time
	epoch  uint32
	fields map[string]*field
}

// field is one field of a machine: its normal in each state and how it is judged now.
type field struct {
	states map[string]*Stat
	cur    *Stat
	at     time.Time
	track
	hint *Anomaly
	// a hint of each kind that ended: until when another waits, and how far it went
	cool [2]cooldown
	// the machine's epoch it settled in
	epoch uint32
	// as they were before the last reading, to take it back
	before      Stat
	beforeTrack track
	beforeHint  Anomaly
}

type cooldown struct {
	until time.Time
	peak  float64
}

// track is how a field is judged: the last reading and how far it and the average were; since
// when (and for how many readings in a row) it has been beyond K σ, its average beyond DriftK, and
// back within; whether it is settling after a gap, and whether its run was held back.
type track struct {
	value, z, zd                  float64
	peak, peakD                   float64
	outSince, driftSince, inSince time.Time
	outN, driftN                  int
	settling                      bool
	settleSince                   time.Time
	held                          bool
	// midFrom: when the minute average last started over; lastOut: the last reading out
	midFrom, lastOut time.Time
}

// valve keeps count of a company's hints: open now, begun in each of the last 60 minutes, and
// whether new ones are held back.
type valve struct {
	open       int
	minutes    [60]uint16
	stamps     [60]int64
	paused     bool
	suppressed int
}

func (v *valve) begun(at time.Time) {
	m := at.UnixMilli() / 60000
	i := m % 60
	if v.stamps[i] != m {
		v.stamps[i], v.minutes[i] = m, 0
	}
	v.minutes[i]++
}

func (v *valve) lastHour(at time.Time) int {
	m := at.UnixMilli() / 60000
	n := 0
	for i, s := range v.stamps {
		if s > m-60 && s <= m {
			n += int(v.minutes[i])
		}
	}
	return n
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
	// Changed is told the company a hint began or ended in, once it is kept (nil: no one).
	Changed func(company string)

	store     *Store
	skip      map[string]bool
	prefixes  []string
	mu        sync.Mutex
	machines  map[mkey]*machine
	companies map[string]*valve
	nextID    int64
	events    chan event
	dropped   atomic.Uint64
	cancel    context.CancelFunc
	wg        sync.WaitGroup
}

// New is a detector keeping its normals and hints in st (nil: nothing kept), loaded from it.
func New(st *Store, set Settings, trail *audit.Log, log *slog.Logger) (*Detector, error) {
	if log == nil {
		log = slog.Default()
	}
	d := &Detector{Set: set, Audit: trail, Log: log, Now: time.Now, store: st, skip: map[string]bool{}, machines: map[mkey]*machine{}, companies: map[string]*valve{},
		events: make(chan event, 1024)}
	for _, f := range append(slices.Clone(skipped), set.Skip...) {
		if p, ok := strings.CutSuffix(f, "*"); ok {
			d.prefixes = append(d.prefixes, p)
		} else if strings.HasSuffix(f, ".") {
			d.prefixes = append(d.prefixes, f)
		} else {
			d.skip[f] = true
		}
	}
	if st != nil {
		rows, err := st.baselines()
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			s := r.stat
			m := d.machineOf(mkey{r.company, r.plant, r.asset})
			m.stated = m.stated || r.state != ""
			d.fieldOf(m, r.field).states[r.state] = &s
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

// skipped says whether a field is never judged.
func (d *Detector) skipped(f string) bool {
	if d.skip[f] {
		return true
	}
	for _, p := range d.prefixes {
		if strings.HasPrefix(f, p) {
			return true
		}
	}
	return false
}

func (d *Detector) valveOf(company string) *valve {
	v := d.companies[company]
	if v == nil {
		v = &valve{}
		d.companies[company] = v
	}
	return v
}

// see notes a reading of the machine: one after a gap, or from another source, starts an epoch.
func (d *Detector) see(m *machine, t tags.Tag, at time.Time) {
	if m.seen.IsZero() || at.Sub(m.seen) > d.Set.Gap || (m.source != "" && t.Source != m.source) {
		m.epoch++
	}
	if at.After(m.seen) {
		m.seen = at
	}
	m.source = t.Source
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
	// what people write down at the machine (checks.Source) is not the machine's own reading
	if t.Source == "manual" || t.Q&tags.Doubtful != 0 || (!t.At.IsZero() && d.Now().Sub(t.At) > tags.Late) {
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
		m.stated = true
		d.see(m, t, at)
		if s != m.state {
			d.switchState(m, s, at)
		}
		d.mu.Unlock()
		return
	}
	x, ok := number(t.Value)
	if !ok || math.IsNaN(x) || math.IsInf(x, 0) || d.skipped(t.Field) {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	m := d.machineOf(mkey{t.Company, t.Plant, t.Asset})
	d.see(m, t, at)
	if (m.stated && m.state == "") || at.Sub(m.since) < settle {
		return
	}
	f := d.fieldOf(m, t.Field)
	s := f.states[m.state]
	if s == nil {
		s = &Stat{First: at.UnixMilli()}
		f.states[m.state] = s
	}
	d.judge(t, m, f, s, x, at)
}

// settle is how long after a change of state readings are left out: a motor spinning down, a
// heater still drawing, are neither the old state's normal nor the new one's.
const settle = 5 * time.Second

// longState is how long a machine has to have been in a state for its fields to settle again
// when it leaves it: a press idle for a minute is still warm, one stopped for half an hour is not.
const longState = 5 * time.Minute

// sameReport is how close readings are that came in one report: the hub stamps each field of a
// message as it publishes it, a few microseconds apart.
const sameReport = 250 * time.Millisecond

// switchState puts the machine in state s. Equipment sends a machine's fields together, in name
// order: the power read with the new state came just before it, and was held against the old
// state's normal. A field read in the same report as the change is taken back: out of that
// normal, and out of the field's hint (an idle machine's power is not a running one's 39 σ low).
func (d *Detector) switchState(m *machine, s string, at time.Time) {
	old := m.state
	if !m.since.IsZero() && at.Sub(m.since) >= longState {
		// after a long stop (or a long run) a temperature takes its time to arrive: settle again
		m.epoch++
	}
	m.state, m.since = s, at
	for _, f := range m.fields {
		if gap := at.Sub(f.at); f.cur == nil || gap < 0 || gap > sameReport || f.cur.N != f.before.N+1 {
			continue
		}
		*f.cur = f.before
		f.cur.dirty = true
		f.track = f.beforeTrack
		if h := f.hint; h != nil && h.ID == f.beforeHint.ID {
			h.Kind, h.Value, h.Z, h.Peak, h.Text = f.beforeHint.Kind, f.beforeHint.Value, f.beforeHint.Z, f.beforeHint.Peak, f.beforeHint.Text
		}
		if f.cur.N == 0 {
			// a reading before the machine first said its state: no state of its own
			delete(f.states, old)
			f.cur = nil
		}
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
// Two tests against it, both in σ of the normal: a reading further than K (a step, an excursion)
// held for Sustain, and the average of the last DriftWindow further than DriftK held for
// DriftSustain (a real shift short of K; a signal that wanders by nature crosses DriftK now and
// then but does not stay there for half an hour). A slow field (a temperature) is held to its own
// pace: both wait at least twice the time it takes to cross its spread, and its average has to go
// where its minute average seldom does. A run, once begun, goes on while it stays a little less
// out (K-1, the drift line less half), so noise near the line does not break it. A hint ends once
// back within for Clear; the same kind then waits Cooldown unless it is much worse.
//
// After a gap (the server restarted, the equipment came back) a machine has moved on: a cooled
// press reads cold while "running". Each field is left out, neither learned nor judged, until its
// reading is back near its normal, or for SettleMax at most.
func (d *Detector) judge(t tags.Tag, m *machine, f *field, s *Stat, x float64, at time.Time) {
	set := d.Set
	f.before, f.beforeTrack = *s, f.track
	if f.hint != nil {
		f.beforeHint = *f.hint
	}
	ms := at.UnixMilli()
	if f.cur != s || f.epoch != m.epoch {
		// another state or another epoch: the evidence so far was against what is past
		s.Short, s.Mid, f.cur, f.midFrom = x, x, s, at
		f.outSince, f.driftSince, f.inSince = time.Time{}, time.Time{}, time.Time{}
		f.outN, f.driftN, f.held = 0, 0, false
		if f.epoch != m.epoch {
			f.epoch, f.settling, f.settleSince = m.epoch, true, at
		}
	}
	var step, dt float64
	if f.cur == s && ms > s.Last {
		dt = float64(ms-s.Last) / 1000
		s.Short += math.Min(1, dt/set.DriftWindow.Seconds()) * (x - s.Short)
		step = math.Min(1, dt/60) * (x - s.Mid)
		s.Mid += step
	}
	warm := s.N >= int64(set.MinReadings) && ms-s.First >= set.Warmup.Milliseconds()
	sigma := s.sigma()
	z := (x - s.Mean) / sigma
	zd := (s.Short - s.Mean) / sigma
	f.value, f.z, f.zd, f.at = x, z, zd, at
	if f.settling {
		if warm && math.Abs(z) > set.K-1 && at.Sub(f.settleSince) < set.SettleMax {
			s.Last = ms
			return
		}
		f.settling = false
		s.Short, s.Mid, f.midFrom = x, x, at
		zd = z
	}
	hold := func(v, k float64, n int) bool { return warm && (math.Abs(v) > k || (n > 0 && math.Abs(v) > k-1)) }
	level := hold(z, set.K, f.outN)
	// a slow field's average strays as far as its readings do: for it the line is where its own
	// minute average seldom goes (K of its spread), so a drift is never just a slow level
	line := math.Max(set.DriftK, set.K*math.Sqrt(s.MidVar)/sigma)
	drift := warm && (math.Abs(zd) > line || (f.driftN > 0 && math.Abs(zd) > line-0.5))
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
	// the pace, from ordinary readings once the minute average has caught up and the mean has
	// something to go by; both parts learned alike from zero, so their ratio is right from the
	// start. Once warm only near the normal, and ten times slower: a field drifting off is not a
	// field that has become slow.
	if w := f.pacing(warm, zd, (s.Mid-s.Mean)/sigma, at); w > 0 && dt > 0 && dt <= 60 && !level && !drift && at.Sub(f.midFrom) >= 2*time.Minute && s.N >= int64(set.MinReadings) {
		s.DVar += w * (step*step/(dt*dt) - s.DVar)
		e := s.Mid - s.Mean
		s.MidVar += w * (e*e - s.MidVar)
	}
	s.Last = ms

	if level || drift {
		f.lastOut = at
	}
	if level {
		if f.outN == 0 {
			f.outSince, f.peak = at, 0
		}
		f.outN++
		if math.Abs(z) > math.Abs(f.peak) {
			f.peak = z
		}
	} else {
		f.outN = 0
	}
	if drift {
		if f.driftN == 0 {
			f.driftSince, f.peakD = at, 0
		}
		f.driftN++
		if math.Abs(zd) > math.Abs(f.peakD) {
			f.peakD = zd
		}
	} else {
		f.driftN = 0
	}
	// back within: no run of three readings out (a lone reading out does not count). A level hint
	// asks only that its readings be back: the average a spike leaves behind is no reason to keep it.
	out := f.outN >= 3 || f.driftN >= 3
	if h := f.hint; h != nil && h.Kind == Level {
		out = f.outN >= 3
	}
	if out || !warm {
		f.inSince = time.Time{}
	} else if f.inSince.IsZero() {
		f.inSince = at
	}
	// a field that takes long to wander takes as long to come back: a press still cold half an
	// hour after a stop is how it warms up, not a hint. Both tests wait twice its pace at least.
	pace := min(2*s.pace(), set.SettleMax)
	kind := ""
	switch {
	case f.outN >= 3 && at.Sub(f.outSince) >= max(set.Sustain, pace):
		kind = Level
	case f.driftN >= 3 && at.Sub(f.driftSince) >= max(set.DriftSustain, pace):
		kind = Drift
	}
	if h := f.hint; h != nil {
		if h.State != m.state {
			// held against another state's normal: it says nothing of this hint
			return
		}
		h.Value, h.Z = x, z
		if h.Kind == Drift && kind != Level {
			h.Value, h.Z = s.Short, zd
		}
		if math.Abs(h.Z) > math.Abs(h.Peak) {
			h.Peak = h.Z
		}
		if kind == Level && h.Kind == Drift {
			// the drift went on to readings K σ out
			h.Kind = Level
			h.Text = Describe(*h)
		}
		if !f.inSince.IsZero() && at.Sub(f.inSince) >= set.Clear {
			end := at
			h.Ended = &end
			f.hint = nil
			f.cool[kindIndex(h.Kind)] = cooldown{until: at.Add(set.Cooldown), peak: math.Abs(h.Peak)}
			d.valveOf(h.Company).open--
			d.emit(event{ended: true, a: *h})
		}
		return
	}
	if kind == "" {
		f.held = false
		return
	}
	now := math.Abs(z)
	if kind == Drift {
		now = math.Abs(zd)
	}
	// the same kind just ended: a run begun before the cooldown is over waits, unless it is much
	// worse than the last; one begun after it has had the full Sustain (a drift half an hour) to
	// show it is new
	since := f.outSince
	if kind == Drift {
		since = f.driftSince
	}
	if c := f.cool[kindIndex(kind)]; since.Before(c.until) && now < math.Max(1.5*c.peak, c.peak+2) {
		return
	}
	v := d.valveOf(t.Company)
	if !d.letThrough(v, t.Company, at) {
		if !f.held {
			f.held = true
			v.suppressed++
		}
		return
	}
	d.nextID++
	h := &Anomaly{ID: d.nextID, Company: t.Company, Plant: t.Plant, Asset: t.Asset, Field: t.Field, State: m.state, Kind: kind,
		Since: f.outSince, Value: x, Z: z, Mean: s.Mean, Sigma: sigma, Source: t.Source}
	h.Peak = f.peak
	if kind == Drift {
		h.Since, h.Value, h.Z, h.Peak = f.driftSince, s.Short, zd, f.peakD
	}
	h.Text = Describe(*h)
	f.hint, f.held = h, false
	v.open++
	v.begun(at)
	d.emit(event{a: *h})
}

// pacing is the weight a reading has in a field's pace: once warm, only a quiet field's (no hint,
// nothing out for ten minutes, its averages near the normal) and ten times less.
func (f *field) pacing(warm bool, zd, zm float64, at time.Time) float64 {
	switch {
	case !warm:
		return 1.0 / 2000
	case f.hint == nil && at.Sub(f.lastOut) >= 10*time.Minute && math.Abs(zd) < 1 && math.Abs(zm) < 2:
		return 1.0 / 20000
	}
	return 0
}

func kindIndex(kind string) int {
	if kind == Drift {
		return 1
	}
	return 0
}

// letThrough says whether a company may have another hint. MaxOpen open or MaxPerHour begun in
// the last hour means its normals are off (learned through a bad day, a fleet that changed), not
// all its machines: new hints wait, said once in the log, until both are down to half.
func (d *Detector) letThrough(v *valve, company string, at time.Time) bool {
	hour := v.lastHour(at)
	switch {
	case !v.paused && (v.open >= d.Set.MaxOpen || hour >= d.Set.MaxPerHour):
		v.paused = true
		d.Log.Warn("anomaly: too many hints, new ones wait until there are fewer; the normals may need learning again", "company", company, "open", v.open, "lastHour", hour)
	case v.paused && v.open < d.Set.MaxOpen/2 && hour < d.Set.MaxPerHour/2:
		v.paused = false
		d.Log.Info("anomaly: hints let through again", "company", company, "open", v.open, "lastHour", hour, "heldBack", v.suppressed)
	}
	return !v.paused
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
	// Z is the last reading's distance, in σ, from the normal it was held against: Now, that of
	// the state the machine was in (absent until a reading in a state). States are every state's.
	Z       float64    `json:"z"`
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
			if s == f.cur && s.N > 0 {
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

// Report is a company's hints now, those of the last 30 days, its measured machines' normals,
// how they are judged and whether new hints are held back.
type Report struct {
	Current  []Anomaly `json:"current"`
	Recent   []Anomaly `json:"recent"`
	Machines []Machine `json:"machines"`
	Settings struct {
		K      float64 `json:"k"`
		DriftK float64 `json:"driftK"`
		// Sustain, DriftSustain, Clear, Cooldown and Warmup in seconds; Span in readings.
		Sustain      float64 `json:"sustain"`
		DriftSustain float64 `json:"driftSustain"`
		Clear        float64 `json:"clear"`
		Cooldown     float64 `json:"cooldown"`
		Warmup       float64 `json:"warmup"`
		Span         int     `json:"span"`
	} `json:"settings"`
	Valve Valve `json:"valve"`
}

// Valve is how many hints a company has: open now and begun in the last hour, against the limits
// past which new ones wait; and how many waited.
type Valve struct {
	Open       int  `json:"open"`
	LastHour   int  `json:"lastHour"`
	MaxOpen    int  `json:"maxOpen"`
	MaxPerHour int  `json:"maxPerHour"`
	Paused     bool `json:"paused"`
	HeldBack   int  `json:"heldBack"`
}

func (d *Detector) Report(company string) (*Report, error) {
	recent, err := d.Recent(company, 100)
	if err != nil {
		return nil, err
	}
	r := &Report{Current: d.Current(company), Recent: recent, Machines: d.Machines(company)}
	set := d.Set
	r.Settings.K, r.Settings.DriftK, r.Settings.Sustain, r.Settings.DriftSustain = set.K, set.DriftK, set.Sustain.Seconds(), set.DriftSustain.Seconds()
	r.Settings.Clear, r.Settings.Cooldown, r.Settings.Warmup, r.Settings.Span = set.Clear.Seconds(), set.Cooldown.Seconds(), set.Warmup.Seconds(), set.Span
	d.mu.Lock()
	v := d.valveOf(company)
	r.Valve = Valve{Open: v.open, LastHour: v.lastHour(d.Now()), MaxOpen: set.MaxOpen, MaxPerHour: set.MaxPerHour, Paused: v.paused, HeldBack: v.suppressed}
	d.mu.Unlock()
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
		d.valveOf(company).open -= len(ended)
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
	if len(ended) > 0 && d.Changed != nil {
		d.Changed(company)
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
	} else if d.Changed != nil {
		d.Changed(a.Company)
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
