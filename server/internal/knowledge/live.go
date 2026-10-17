package knowledge

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"smartfactory/internal/anomaly"
	"smartfactory/internal/audit"
	"smartfactory/internal/auth"
	"smartfactory/internal/engine"
	"smartfactory/internal/tags"
)

// Live holds the ontology's limits on readings against what equipment sends. A constraint on a
// class with a field and limits ("冷藏设备 temp ≤ 4") is compiled, each time the ontology or the
// process changes, into one watch per station of that class; a reading finds its watches in one
// map lookup, without allocating, and only a reading of a watched field of a watched station does
// more. A machine no equipment reports for is judged by the twin's simulated values instead, every
// few seconds (Sim). Out of range for the constraint's seconds (5 by default), the watch fires: an alarm on the
// machine in the twin, an entry in the activity log, and a record that goes into the knowledge
// graph beside the station. Back in range, the alarm clears and the record ends. Firing and
// clearing are done off the reading's path (Run).
type Live struct {
	// Alert raises (text) or clears ("") an alarm on a machine; nil raises none.
	Alert func(company, plant, asset, code, text string) bool
	// Sim is the twin's simulated values of fields of machines (nil for one its equipment reports
	// for); nil judges readings from equipment only.
	Sim   func(company string, keys [][3]string) []*float64
	Audit *audit.Log
	// Changed is told the company a record began or ended in.
	Changed func(company string)
	Log     *slog.Logger
	Now     func() time.Time

	dir   string
	index atomic.Pointer[map[lkey][]*watch]
	kick  chan struct{}

	mu      sync.Mutex
	watches map[string][]*watch // by company
	records map[string]*liveRecords
}

type lkey struct{ company, plant, asset, field string }

// watch is one constraint on one field of one station.
type watch struct {
	key  lkey
	name string // the station as the graph names it
	c    OntConstraint
	lo   float64
	hi   float64
	hold time.Duration

	mu sync.Mutex
	// out of range since (zero: in range), the last reading and the furthest out
	since       time.Time
	last, worst float64
	firing      bool
	// reported is what the alarm and the record say; rec the record while it fires
	reported bool
	rec      int64
}

// liveDefaultFor is how long a reading stays out of range before it counts, by default.
const liveDefaultFor = 5 * time.Second

// LiveRecord is a time a station's reading was out of a constraint's range.
type LiveRecord struct {
	ID         int64      `json:"id"`
	Constraint string     `json:"constraint"`
	Class      string     `json:"class"`
	Plant      string     `json:"plant"`
	Station    string     `json:"station"`
	Name       string     `json:"name"`
	Field      string     `json:"field"`
	Min        *float64   `json:"min,omitempty"`
	Max        *float64   `json:"max,omitempty"`
	Value      float64    `json:"value"`
	Worst      float64    `json:"worst"`
	Since      time.Time  `json:"since"`
	Ended      *time.Time `json:"ended,omitempty"`
	Text       string     `json:"text"`
}

type liveRecords struct {
	Seq   int64        `json:"seq"`
	Items []LiveRecord `json:"items"`
}

func NewLive(dir string, log *slog.Logger) *Live {
	if log == nil {
		log = slog.Default()
	}
	return &Live{dir: dir, Log: log, Now: time.Now, kick: make(chan struct{}, 1), watches: map[string][]*watch{}, records: map[string]*liveRecords{}}
}

// Add judges one reading. It is the hub's Tap: it must not block, and for a field nobody
// constrains it is a map lookup.
func (l *Live) Add(t tags.Tag) {
	idx := l.index.Load()
	if idx == nil {
		return
	}
	ws := (*idx)[lkey{t.Company, t.Plant, t.Asset, t.Field}]
	if len(ws) == 0 || t.Source == "manual" || t.Q&tags.Doubtful != 0 {
		return
	}
	x, ok := liveNumber(t.Value)
	if !ok || math.IsNaN(x) || math.IsInf(x, 0) {
		return
	}
	at := t.At
	if at.IsZero() {
		at = l.Now()
	} else if l.Now().Sub(at) > tags.Late {
		return
	}
	changed := false
	for _, w := range ws {
		changed = w.see(x, at) || changed
	}
	if changed {
		select {
		case l.kick <- struct{}{}:
		default:
		}
	}
}

func liveNumber(v any) (float64, bool) {
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

// see takes a reading; true when the watch began or stopped firing.
func (w *watch) see(x float64, at time.Time) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.last = x
	if x >= w.lo && x <= w.hi {
		w.since = time.Time{}
		if w.firing {
			w.firing = false
			return true
		}
		return false
	}
	if w.since.IsZero() {
		w.since, w.worst = at, x
	} else if w.beyond(x) > w.beyond(w.worst) {
		w.worst = x
	}
	if !w.firing && at.Sub(w.since) >= w.hold {
		w.firing = true
		return true
	}
	return false
}

// beyond is how far x is out of range.
func (w *watch) beyond(x float64) float64 { return math.Max(x-w.hi, w.lo-x) }

// liveWatches are the watches the company's ontology puts on its stations: one per constraint
// with a field, on every station whose classes (bound, given to it as a named thing, and what
// those are kinds of) include the constraint's.
func liveWatches(m *Model) []*watch {
	if m.Ontology == nil {
		return nil
	}
	var cs []OntConstraint
	for _, c := range m.Ontology.Constraints {
		if c.Field != "" && (c.Min != nil || c.Max != nil) {
			cs = append(cs, c)
		}
	}
	if len(cs) == 0 {
		return nil
	}
	up := superclasses(m.Ontology)
	named := map[string][]string{}
	for _, ind := range m.Ontology.Individuals {
		named[ind.Name] = append(named[ind.Name], ind.A...)
	}
	n := nameModel(m)
	var out []*watch
	for _, p := range m.Plants {
		for _, l := range p.Lines {
			for _, s := range l.Stations {
				name := n.station[p.ID+"/"+s.ID]
				is := up(append(append([]string{"工位"}, s.Classes...), named[name]...))
				for _, c := range cs {
					if !is[c.Class] {
						continue
					}
					w := &watch{key: lkey{m.ID, p.ID, s.ID, c.Field}, name: name, c: c, lo: math.Inf(-1), hi: math.Inf(1), hold: liveDefaultFor}
					if c.Min != nil {
						w.lo = *c.Min
					}
					if c.Max != nil {
						w.hi = *c.Max
					}
					if c.For != nil {
						w.hold = time.Duration(*c.For * float64(time.Second))
					}
					out = append(out, w)
				}
			}
		}
	}
	return out
}

// superclasses gives the classes things of some classes are: those and every class above them.
func superclasses(o *Ontology) func([]string) map[string]bool {
	sub := map[string][]string{}
	for _, c := range o.Classes {
		sub[c.Name] = append(sub[c.Name], c.Sub...)
	}
	return func(start []string) map[string]bool {
		is := map[string]bool{}
		todo := slices.Clone(start)
		for len(todo) > 0 {
			c := todo[len(todo)-1]
			todo = todo[:len(todo)-1]
			if c == "" || is[c] {
				continue
			}
			is[c] = true
			todo = append(todo, sub[c]...)
		}
		return is
	}
}

// same is whether two watches watch the same thing the same way (so the old keeps its state).
func (w *watch) same(o *watch) bool {
	a, _ := json.Marshal(w.c)
	b, _ := json.Marshal(o.c)
	return w.key == o.key && w.name == o.name && string(a) == string(b)
}

// Set puts the company's watches in place of the ones it had (none: nil). A watch that is the
// same keeps its state; one that goes while it fires ends its record and clears its alarm.
func (l *Live) Set(company string, ws []*watch) {
	l.mu.Lock()
	l.loaded(company)
	old := l.watches[company]
	var gone []*watch
	for i, w := range ws {
		if j := slices.IndexFunc(old, func(o *watch) bool { return o != nil && o.same(w) }); j >= 0 {
			ws[i], old[j] = old[j], nil
		}
	}
	for _, o := range old {
		if o != nil {
			gone = append(gone, o)
		}
	}
	if len(ws) == 0 {
		delete(l.watches, company)
	} else {
		l.watches[company] = ws
	}
	idx := map[lkey][]*watch{}
	for _, cws := range l.watches {
		for _, w := range cws {
			idx[w.key] = append(idx[w.key], w)
		}
	}
	l.index.Store(&idx)
	l.mu.Unlock()
	for _, w := range gone {
		w.mu.Lock()
		reported := w.reported
		w.firing, w.reported = false, false
		w.mu.Unlock()
		if reported {
			l.end(w, "约束改了，不再看这台设备")
		}
	}
}

func (l *Live) recordsPath(company string) string {
	return filepath.Join(l.dir, company+".violations.json")
}

// loaded is the company's records, read when first needed; those left open by the last run are
// ended now and their alarms cleared (the machine is judged afresh). l.mu is held.
func (l *Live) loaded(company string) *liveRecords {
	if r, ok := l.records[company]; ok {
		return r
	}
	r := &liveRecords{}
	if raw, err := os.ReadFile(l.recordsPath(company)); err == nil {
		json.Unmarshal(raw, r)
	}
	l.records[company] = r
	now := l.Now()
	ended := false
	for i := range r.Items {
		if it := &r.Items[i]; it.Ended == nil {
			it.Ended = &now
			ended = true
			if l.Alert != nil {
				l.Alert(company, it.Plant, it.Station, "rule:"+it.Constraint, "")
			}
		}
	}
	if ended {
		l.save(company, r)
	}
	return r
}

func (l *Live) save(company string, r *liveRecords) {
	if !engine.ValidID.MatchString(company) {
		return
	}
	// what is kept: the last 30 days, at most perKind, those still open whatever their age
	now := l.Now()
	keep := r.Items[:0]
	for _, it := range r.Items {
		if it.Ended == nil || now.Sub(it.Since) < keepEventDays*24*time.Hour {
			keep = append(keep, it)
		}
	}
	if len(keep) > perKind {
		keep = keep[len(keep)-perKind:]
	}
	r.Items = keep
	raw, _ := json.Marshal(r)
	if err := os.WriteFile(l.recordsPath(company), raw, 0o644); err != nil {
		l.Log.Warn("ontology: records", "company", company, "err", err)
	}
}

// simEvery is how often the twin's own values are judged, for machines no equipment reports for.
const simEvery = 2 * time.Second

// Run fires and clears the watches whose readings went out of range or came back, until ctx ends;
// with Sim, it also judges the twin's simulated values of the watched machines every few seconds.
func (l *Live) Run(ctx context.Context) {
	tick := time.NewTicker(simEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-l.kick:
			l.reconcile()
		case <-tick.C:
			if l.sample() {
				l.reconcile()
			}
		}
	}
}

// sample judges the twin's values of every watched machine its equipment does not report for
// (those come through Add); true when a watch began or stopped firing.
func (l *Live) sample() bool {
	if l.Sim == nil {
		return false
	}
	l.mu.Lock()
	byCompany := map[string][]*watch{}
	for c, ws := range l.watches {
		byCompany[c] = slices.Clone(ws)
	}
	l.mu.Unlock()
	changed := false
	now := l.Now()
	for c, ws := range byCompany {
		keys := make([][3]string, len(ws))
		for i, w := range ws {
			keys[i] = [3]string{w.key.plant, w.key.asset, w.key.field}
		}
		for i, v := range l.Sim(c, keys) {
			if v != nil && !math.IsNaN(*v) {
				changed = ws[i].see(*v, now) || changed
			}
		}
	}
	return changed
}

func (l *Live) reconcile() {
	l.mu.Lock()
	var all []*watch
	for _, ws := range l.watches {
		all = append(all, ws...)
	}
	l.mu.Unlock()
	for _, w := range all {
		w.mu.Lock()
		firing, reported := w.firing, w.reported
		w.reported = firing
		w.mu.Unlock()
		switch {
		case firing && !reported:
			l.fire(w)
		case !firing && reported:
			l.end(w, "")
		}
	}
}

var ontologyWho = auth.Who{Name: "本体约束"}

func fnumber(x float64) string {
	return strconv.FormatFloat(math.Round(x*1000)/1000, 'f', -1, 64)
}

// seconds is a duration as people say it: 5 秒, 3 分 20 秒.
func seconds(d time.Duration) string {
	s := int(d.Round(time.Second) / time.Second)
	switch {
	case s < 60:
		return fmt.Sprintf("%d 秒", s)
	case s%60 == 0:
		return fmt.Sprintf("%d 分钟", s/60)
	}
	return fmt.Sprintf("%d 分 %d 秒", s/60, s%60)
}

// limits is a constraint's range as people write it: ≤ 4, 30～45, ≥ 2.
func limits(c OntConstraint) string {
	switch {
	case c.Min != nil && c.Max != nil:
		return fnumber(*c.Min) + "～" + fnumber(*c.Max)
	case c.Max != nil:
		return "≤ " + fnumber(*c.Max)
	case c.Min != nil:
		return "≥ " + fnumber(*c.Min)
	}
	return ""
}

func (w *watch) text(value float64) string {
	vars := map[string]string{"name": w.name, "value": fnumber(value), "field": anomaly.FieldLabel(w.c.Field), "class": w.c.Class}
	if w.c.Min != nil {
		vars["min"] = fnumber(*w.c.Min)
	}
	if w.c.Max != nil {
		vars["max"] = fnumber(*w.c.Max)
	}
	if w.c.Say != "" {
		return say(w.c.Say, vars)
	}
	return fmt.Sprintf("%s %s %s，%s要求 %s", w.name, vars["field"], vars["value"], w.c.Class, limits(w.c))
}

func (l *Live) fire(w *watch) {
	w.mu.Lock()
	value, since := w.last, w.since
	w.mu.Unlock()
	if since.IsZero() {
		since = l.Now()
	}
	text := w.text(value)
	k := w.key
	if l.Alert != nil {
		l.Alert(k.company, k.plant, k.asset, "rule:"+w.c.ID, text)
	}
	l.mu.Lock()
	r := l.loaded(k.company)
	r.Seq++
	r.Items = append(r.Items, LiveRecord{ID: r.Seq, Constraint: w.c.ID, Class: w.c.Class, Plant: k.plant, Station: k.asset, Name: w.name, Field: k.field,
		Min: w.c.Min, Max: w.c.Max, Value: value, Worst: value, Since: since, Text: text})
	l.save(k.company, r)
	l.mu.Unlock()
	w.mu.Lock()
	w.rec = r.Seq
	w.mu.Unlock()
	l.Audit.RecordWith(ontologyWho, "ontology", k.company, "ontology.violation", k.plant+"/"+k.asset, text,
		fmt.Sprintf("约束「%s」：%s的%s要求 %s，超出 %s 以上", w.c.ID, w.c.Class, anomaly.FieldLabel(k.field), limits(w.c), seconds(w.hold)), nil)
	if l.Changed != nil {
		l.Changed(k.company)
	}
}

// end ends the watch's record (why: when it is not that the reading came back) and clears its alarm.
func (l *Live) end(w *watch, why string) {
	w.mu.Lock()
	worst, rec := w.worst, w.rec
	w.rec = 0
	w.mu.Unlock()
	k := w.key
	if l.Alert != nil {
		l.Alert(k.company, k.plant, k.asset, "rule:"+w.c.ID, "")
	}
	now := l.Now()
	var since time.Time
	l.mu.Lock()
	r := l.loaded(k.company)
	for i := range r.Items {
		if it := &r.Items[i]; it.ID == rec && it.Ended == nil {
			it.Ended = &now
			if w.beyond(worst) > w.beyond(it.Worst) {
				it.Worst = worst
			}
			worst, since = it.Worst, it.Since
		}
	}
	l.save(k.company, r)
	l.mu.Unlock()
	detail := fmt.Sprintf("%s %s恢复到 %s 以内", w.name, anomaly.FieldLabel(k.field), limits(w.c))
	if why != "" {
		detail = w.name + " " + why
	}
	context := fmt.Sprintf("约束「%s」", w.c.ID)
	if !since.IsZero() {
		context += fmt.Sprintf("，持续 %s，最远 %s", seconds(now.Sub(since)), fnumber(worst))
	}
	l.Audit.RecordWith(ontologyWho, "ontology", k.company, "ontology.violation.end", k.plant+"/"+k.asset, detail, context, nil)
	if l.Changed != nil {
		l.Changed(k.company)
	}
}

// Current is what is out of range now, the longest first.
func (l *Live) Current(company string) []OntViolation {
	l.mu.Lock()
	ws := slices.Clone(l.watches[company])
	l.mu.Unlock()
	out := []OntViolation{}
	for _, w := range ws {
		w.mu.Lock()
		if w.reported {
			v := w.worst
			out = append(out, OntViolation{Constraint: w.c.ID, Name: w.name, Plant: w.key.plant, Field: w.key.field, Value: &v, Since: w.since.UnixMilli(), Text: w.text(w.last)})
		}
		w.mu.Unlock()
	}
	slices.SortFunc(out, func(a, b OntViolation) int { return int(a.Since - b.Since) })
	return out
}

// Watching is how many stations each of the company's live constraints watches.
func (l *Live) Watching(company string) map[string]int {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := map[string]int{}
	for _, w := range l.watches[company] {
		out[w.c.ID]++
	}
	return out
}

// Records are the company's times out of range that began since, the latest first.
func (l *Live) Records(company string, since time.Time) []LiveRecord {
	l.mu.Lock()
	defer l.mu.Unlock()
	r := l.loaded(company)
	var out []LiveRecord
	for _, it := range r.Items {
		if it.Ended == nil || !it.Since.Before(since) {
			out = append(out, it)
		}
	}
	slices.Reverse(out)
	return out
}
