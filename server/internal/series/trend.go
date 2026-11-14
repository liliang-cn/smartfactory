package series

import (
	"database/sql"
	"fmt"
	"math"
	"slices"
	"sort"
	"time"
)

// A trend is what a machine did over a span, summed up for someone who asks about it rather
// than for a chart: each reading's low, high and mean and when, a coarse curve of it, the time
// the machine spent in each state and its longest stops with what stopped it. Downtime ranks
// a company's machines by how long they stood still. The in-app assistant and the MCP server
// both answer from these, so the two say the same.

// Stops are the states in which a machine is not making anything for a reason of its own
// (idle and blocked wait for the line; they are counted, not listed).
var Stops = []string{"fault", "stopped", "maint", "changeover"}

// MaxTrendSpan is the longest span a trend or a ranking covers: what the state changes and the
// minute aggregates are kept for by default.
const MaxTrendSpan = 90 * 24 * time.Hour

// TrendQuery asks for one machine's trend.
type TrendQuery struct {
	Company, Plant, Asset string
	// Fields are the numbers to sum up; none: every number stored for the machine.
	Fields   []string
	From, To time.Time
	// Points is how many intervals each curve has (default 24, at most 96).
	Points int
	// Loc is the time zone the times are given in (default: UTC).
	Loc *time.Location
}

// Trend is one machine's readings and states over a span.
type Trend struct {
	Company string       `json:"company"`
	Plant   string       `json:"plant"`
	Asset   string       `json:"asset"`
	From    time.Time    `json:"from"`
	To      time.Time    `json:"to"`
	Fields  []FieldTrend `json:"fields"`
	States  *States      `json:"states,omitempty"`
	// LastReading is when the machine last sent anything, when that is well before To.
	LastReading *time.Time `json:"last_reading,omitempty"`
	Note        string     `json:"note,omitempty"`
}

// FieldTrend is one number over the span. Curve points are [time, mean, low, high].
type FieldTrend struct {
	Field    string   `json:"field"`
	Min      *Extreme `json:"min,omitempty"`
	Max      *Extreme `json:"max,omitempty"`
	Avg      *float64 `json:"avg,omitempty"`
	First    *float64 `json:"first,omitempty"`
	Last     *float64 `json:"last,omitempty"`
	Readings int      `json:"readings"`
	// Flagged readings were kept but not believed (out of range, a spike): left out of the rest.
	Flagged int     `json:"flagged,omitempty"`
	Curve   [][]any `json:"curve"`
}

// Extreme is a low or a high and the minute it was reached in.
type Extreme struct {
	V  float64   `json:"v"`
	At time.Time `json:"at"`
}

// States is how the machine's time went.
type States struct {
	// Totals are the minutes in each state, the most first.
	Totals []StateTime `json:"totals"`
	// Stops are the longest stops (fault, stopped, maint, changeover), longest first, at most 10.
	Stops []Stop `json:"stops"`
	// StopCount and StopMinutes count every stop of the span.
	StopCount   int     `json:"stop_count"`
	StopMinutes float64 `json:"stop_minutes"`
	// Reasons are the stops by what stopped the machine, the longest in all first.
	Reasons []Reason `json:"reasons,omitempty"`
	Changes int      `json:"changes"`
}

// StateTime is the time in one state.
type StateTime struct {
	State   string  `json:"state"`
	Minutes float64 `json:"minutes"`
	Share   float64 `json:"share"`
}

// Stop is one span the machine stood still.
type Stop struct {
	State   string    `json:"state"`
	From    time.Time `json:"from"`
	To      time.Time `json:"to"`
	Minutes float64   `json:"minutes"`
	Reason  string    `json:"reason,omitempty"`
	// Open: still stopped at the end of the span.
	Open bool `json:"open,omitempty"`
}

// Reason is how often and how long one cause stopped the machine.
type Reason struct {
	Reason  string  `json:"reason"`
	Count   int     `json:"count"`
	Minutes float64 `json:"minutes"`
}

// MachineStops is one machine's stops over a span, for a ranking.
type MachineStops struct {
	Plant       string             `json:"plant"`
	Asset       string             `json:"asset"`
	StopMinutes float64            `json:"stop_minutes"`
	StopCount   int                `json:"stop_count"`
	ByState     map[string]float64 `json:"by_state"`
	Longest     *Stop              `json:"longest,omitempty"`
	Reasons     []Reason           `json:"reasons,omitempty"`
	// RunShare is the share of the span the machine ran.
	RunShare float64 `json:"run_share"`
}

// silent is how long a machine may send nothing before its last state stops being believed.
const silent = 15 * time.Minute

func (q *TrendQuery) span(now time.Time) error {
	if q.To.IsZero() || q.To.After(now) {
		q.To = now
	}
	if q.From.IsZero() {
		q.From = q.To.Add(-8 * time.Hour)
	}
	if !q.From.Before(q.To) {
		return fmt.Errorf("开始时间要早于结束时间")
	}
	if q.To.Sub(q.From) > MaxTrendSpan {
		return fmt.Errorf("一次最多看 %d 天", int(MaxTrendSpan.Hours()/24))
	}
	if q.Loc == nil {
		q.Loc = time.UTC
	}
	return nil
}

// Trend sums up one machine over a span.
func (s *Store) Trend(q TrendQuery) (*Trend, error) {
	if err := q.span(s.Now()); err != nil {
		return nil, err
	}
	all, err := s.fields(q.Company, q.Plant, q.Asset)
	if err != nil {
		return nil, err
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("没有存 %s 的读数", q.Asset)
	}
	loc := q.Loc
	from, to := q.From.UnixMilli(), q.To.UnixMilli()
	tr := &Trend{Company: q.Company, Plant: q.Plant, Asset: q.Asset, From: q.From.In(loc), To: q.To.In(loc), Fields: []FieldTrend{}}

	// the numbers come by the minute while minutes are kept, by the hour before that
	res, width := Minute, minute
	if q.From.Before(s.Now().Add(-s.ret.Minute)) {
		res, width = Hour, hour
	}
	points := q.Points
	if points <= 0 {
		points = 24
	}
	points = min(points, 96)
	// curve intervals are a round number of minutes or hours, on the clock
	need := max(width, (to-from+int64(points)-1)/int64(points))
	bucket := int64(24 * hour)
	for _, m := range []int64{1, 2, 5, 10, 15, 20, 30, 60, 120, 180, 240, 360, 720, 1440} {
		if m*minute >= need && m*minute >= width {
			bucket = m * minute
			break
		}
	}
	if need > bucket {
		bucket = (need + day - 1) / day * day
	}

	fields := q.Fields
	if len(fields) == 0 {
		for f := range all {
			fields = append(fields, f)
		}
		sort.Strings(fields)
	}
	var last int64
	var unknown []string
	for _, f := range fields {
		sid, ok := all[f]
		if !ok {
			unknown = append(unknown, f)
			continue
		}
		as, err := s.aggs(sid, res, from, to)
		if err != nil {
			return nil, err
		}
		if len(as) == 0 {
			continue // a field of words (state, fault): its changes are below
		}
		ft := FieldTrend{Field: f, Curve: [][]any{}}
		var sum float64
		var cur *Agg
		flush := func() {
			if cur != nil && cur.N > 0 {
				ft.Curve = append(ft.Curve, []any{time.UnixMilli(cur.T).In(loc), round(cur.Avg()), round(cur.Min), round(cur.Max)})
			}
		}
		for _, a := range as {
			ft.Flagged += a.Flagged
			if a.N == 0 {
				continue
			}
			last = max(last, a.T+width)
			ft.Readings += a.N
			sum += a.Sum
			at := time.UnixMilli(a.T).In(loc)
			if ft.Min == nil || a.Min < ft.Min.V {
				ft.Min = &Extreme{round(a.Min), at}
			}
			if ft.Max == nil || a.Max > ft.Max.V {
				ft.Max = &Extreme{round(a.Max), at}
			}
			if ft.First == nil {
				v := round(a.Sum / float64(a.N))
				ft.First = &v
			}
			l := round(a.Last)
			ft.Last = &l
			b := a.T / bucket * bucket
			if cur == nil || cur.T != b {
				flush()
				cur = &Agg{T: b, Min: math.Inf(1), Max: math.Inf(-1)}
			}
			*cur = combine(b, []Agg{*cur, a})
		}
		flush()
		if ft.Readings == 0 {
			if ft.Flagged > 0 {
				tr.Fields = append(tr.Fields, ft)
			}
			continue
		}
		avg := round(sum / float64(ft.Readings))
		ft.Avg = &avg
		tr.Fields = append(tr.Fields, ft)
	}

	if sid, ok := all["state"]; ok {
		var why int64
		if id, ok := all["fault"]; ok {
			why = id
		}
		st, end, err := s.timeline(sid, why, from, to, last)
		if err != nil {
			return nil, err
		}
		last = max(last, end)
		tr.States = st.summary(loc)
	}
	if last > 0 && time.UnixMilli(last).Before(q.To.Add(-silent)) {
		t := time.UnixMilli(last).In(loc)
		tr.LastReading = &t
		tr.Note = fmt.Sprintf("这台设备 %s 以后没有再发数据，之后的情况不知道", t.Format("01-02 15:04"))
	}
	if last == 0 && tr.States == nil {
		tr.Note = "这段时间这台设备没有发来数据"
	}
	if len(unknown) > 0 {
		names := make([]string, 0, len(all))
		for f := range all {
			names = append(names, f)
		}
		sort.Strings(names)
		tr.Note = joinNote(tr.Note, fmt.Sprintf("没有存 %v 这些读数；这台设备有 %v", unknown, names))
	}
	return tr, nil
}

func joinNote(a, b string) string {
	if a == "" {
		return b
	}
	return a + "；" + b
}

// Downtime ranks a company's machines (of one plant, or every plant) by how long they stood
// still over a span, the longest first; machines that never stopped come last, by how little
// they ran.
func (s *Store) Downtime(company, plant string, from, to time.Time, loc *time.Location) ([]MachineStops, error) {
	q := TrendQuery{From: from, To: to, Loc: loc}
	if err := q.span(s.Now()); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT a.plant, a.asset, a.id, coalesce(f.id, 0) FROM series a LEFT JOIN series f
		ON f.company = a.company AND f.plant = a.plant AND f.asset = a.asset AND f.field = 'fault'
		WHERE a.company = ? AND a.field = 'state' AND (? = '' OR a.plant = ?)`, company, plant, plant)
	if err != nil {
		return nil, err
	}
	type machine struct {
		plant, asset string
		sid, why     int64
	}
	var ms []machine
	for rows.Next() {
		var m machine
		rows.Scan(&m.plant, &m.asset, &m.sid, &m.why)
		ms = append(ms, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	f, t := q.From.UnixMilli(), q.To.UnixMilli()
	out := []MachineStops{}
	for _, m := range ms {
		st, _, err := s.timeline(m.sid, m.why, f, t, 0)
		if err != nil {
			return nil, err
		}
		if st.total == 0 {
			continue
		}
		sum := st.summary(q.Loc)
		r := MachineStops{Plant: m.plant, Asset: m.asset, StopMinutes: sum.StopMinutes, StopCount: sum.StopCount, ByState: map[string]float64{}, Reasons: sum.Reasons}
		for _, x := range sum.Totals {
			if slices.Contains(Stops, x.State) {
				r.ByState[x.State] = x.Minutes
			}
			if x.State == "running" {
				r.RunShare = x.Share
			}
		}
		if len(sum.Stops) > 0 {
			r.Longest = &sum.Stops[0]
		}
		if len(r.Reasons) > 3 {
			r.Reasons = r.Reasons[:3]
		}
		out = append(out, r)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].StopMinutes != out[j].StopMinutes {
			return out[i].StopMinutes > out[j].StopMinutes
		}
		return out[i].RunShare < out[j].RunShare
	})
	return out, nil
}

// timeline is a machine's states over a span, as worked out from the changes stored.
type timeline struct {
	ms    map[string]int64 // milliseconds in each state
	stops []stopAt
	total int64
	turns int
}

type stopAt struct {
	state    string
	from, to int64
	reason   string
	open     bool
}

// timeline walks the state changes of series sid (and the fault texts of series why, 0: none)
// over [from, to]. A machine's last state holds until it changes, or until it last sent
// anything (seen, when known) if that is well before to. It returns the timeline and the time
// of the last change it read.
func (s *Store) timeline(sid, why, from, to, seen int64) (*timeline, int64, error) {
	tl := &timeline{ms: map[string]int64{}}
	before := func(id int64) (string, error) {
		var v string
		err := s.db.QueryRow(`SELECT v FROM events WHERE sid = ? AND t < ? ORDER BY t DESC LIMIT 1`, id, from).Scan(&v)
		if err == sql.ErrNoRows {
			return "", nil
		}
		return v, err
	}
	type change struct {
		t      int64
		v      string
		reason bool
	}
	var cs []change
	read := func(id int64, reason bool) error {
		rows, err := s.db.Query(`SELECT t, v FROM events WHERE sid = ? AND t >= ? AND t <= ? ORDER BY t`, id, from, to)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			c := change{reason: reason}
			rows.Scan(&c.t, &c.v)
			cs = append(cs, c)
		}
		return rows.Err()
	}
	state, err := before(sid)
	if err != nil {
		return nil, 0, err
	}
	if err := read(sid, false); err != nil {
		return nil, 0, err
	}
	var reason string
	if why != 0 {
		if reason, err = before(why); err != nil {
			return nil, 0, err
		}
		if err := read(why, true); err != nil {
			return nil, 0, err
		}
	}
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].t < cs[j].t })
	var lastChange int64
	for _, c := range cs {
		lastChange = max(lastChange, c.t)
	}
	end := to
	if seen > 0 && max(seen, lastChange) < to-int64(silent/time.Millisecond) {
		end = max(seen, lastChange)
	}

	at := from
	var cur *stopAt
	closeStop := func(t int64, open bool) {
		if cur != nil {
			cur.to, cur.open = t, open
			tl.stops = append(tl.stops, *cur)
			cur = nil
		}
	}
	enter := func(t int64, st string) {
		if slices.Contains(Stops, st) {
			if cur == nil || cur.state != st {
				closeStop(t, false)
				cur = &stopAt{state: st, from: t, reason: reason}
			}
		} else {
			closeStop(t, false)
		}
	}
	if state != "" {
		enter(from, state)
	}
	for _, c := range cs {
		if c.t > end {
			break
		}
		if c.reason {
			reason = c.v
			if cur != nil && cur.reason == "" {
				cur.reason = c.v
			}
			continue
		}
		if state != "" {
			tl.ms[state] += c.t - at
			tl.total += c.t - at
		}
		if c.v != state {
			tl.turns++
		}
		state, at = c.v, c.t
		enter(c.t, state)
	}
	if state != "" && end > at {
		tl.ms[state] += end - at
		tl.total += end - at
	}
	closeStop(end, end == to)
	return tl, lastChange, nil
}

func (tl *timeline) summary(loc *time.Location) *States {
	st := &States{Totals: []StateTime{}, Stops: []Stop{}, Changes: tl.turns}
	for k, v := range tl.ms {
		share := 0.0
		if tl.total > 0 {
			share = math.Round(float64(v)/float64(tl.total)*1000) / 1000
		}
		st.Totals = append(st.Totals, StateTime{State: k, Minutes: mins(v), Share: share})
	}
	sort.Slice(st.Totals, func(i, j int) bool {
		if st.Totals[i].Minutes != st.Totals[j].Minutes {
			return st.Totals[i].Minutes > st.Totals[j].Minutes
		}
		return st.Totals[i].State < st.Totals[j].State
	})
	reasons := map[string]*Reason{}
	var stopMS int64
	for _, x := range tl.stops {
		stopMS += x.to - x.from
		key := x.reason
		if key == "" {
			key = x.state
		}
		r := reasons[key]
		if r == nil {
			r = &Reason{Reason: key}
			reasons[key] = r
		}
		r.Count++
		r.Minutes += float64(x.to-x.from) / 60000
		st.Stops = append(st.Stops, Stop{State: x.state, From: time.UnixMilli(x.from).In(loc), To: time.UnixMilli(x.to).In(loc), Minutes: mins(x.to - x.from), Reason: x.reason, Open: x.open})
	}
	st.StopCount, st.StopMinutes = len(tl.stops), mins(stopMS)
	sort.SliceStable(st.Stops, func(i, j int) bool { return st.Stops[i].Minutes > st.Stops[j].Minutes })
	if len(st.Stops) > 10 {
		st.Stops = st.Stops[:10]
	}
	for _, r := range reasons {
		r.Minutes = math.Round(r.Minutes*10) / 10
		st.Reasons = append(st.Reasons, *r)
	}
	sort.Slice(st.Reasons, func(i, j int) bool {
		if st.Reasons[i].Minutes != st.Reasons[j].Minutes {
			return st.Reasons[i].Minutes > st.Reasons[j].Minutes
		}
		return st.Reasons[i].Reason < st.Reasons[j].Reason
	})
	if len(st.Reasons) > 8 {
		st.Reasons = st.Reasons[:8]
	}
	return st
}

func mins(ms int64) float64 { return math.Round(float64(ms)/6000) / 10 }

// Machines are a company's machines that have stored readings, as plant and asset.
func (s *Store) Machines(company string) ([][2]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT plant, asset FROM series WHERE company=? ORDER BY 1, 2`, company)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out [][2]string
	for rows.Next() {
		var m [2]string
		rows.Scan(&m[0], &m[1])
		out = append(out, m)
	}
	return out, rows.Err()
}

// Fields are the fields stored for a machine, sorted.
func (s *Store) Fields(company, plant, asset string) ([]string, error) {
	all, err := s.fields(company, plant, asset)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(all))
	for f := range all {
		out = append(out, f)
	}
	sort.Strings(out)
	return out, nil
}
