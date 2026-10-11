package series

import (
	"fmt"
	"math"
	"sort"
	"time"

	"smartfactory/internal/tags"
)

// Resolutions of an answer.
const (
	Raw    = "raw"
	Minute = "minute"
	Hour   = "hour"
)

// MaxRaw is the most raw readings a field returns; past it an automatic answer comes in minutes
// and a forced raw one is cut to the latest.
const MaxRaw = 20000

// Query asks for an asset's readings.
type Query struct {
	Company, Plant, Asset string
	// Fields to return; none: every field stored for the asset.
	Fields   []string
	From, To time.Time
	// Step forces a resolution (raw, minute, hour); empty picks one by the range.
	Step string
}

// Answer is an asset's readings over a range. Raw points are [t, value, flags]; minute and
// hour points are [t, avg, min, max, readings, flagged] (avg, min and max null when every
// reading of the interval was flagged). Times are unix milliseconds.
type Answer struct {
	Company    string   `json:"company"`
	Plant      string   `json:"plant"`
	Asset      string   `json:"asset"`
	From       int64    `json:"from"`
	To         int64    `json:"to"`
	Resolution string   `json:"resolution"`
	Series     []Field  `json:"series"`
	Events     []Event  `json:"events"`
	Truncated  bool     `json:"truncated,omitempty"`
	Fields     []string `json:"fields"`
}

// Field is one field's points.
type Field struct {
	Field  string  `json:"field"`
	Points [][]any `json:"points"`
}

// Event is a state word or a text a device sent (stored when it changed).
type Event struct {
	Field string `json:"field"`
	T     int64  `json:"t"`
	V     string `json:"v"`
	Src   string `json:"src"`
	Q     uint8  `json:"q,omitempty"`
}

// Read answers a query.
func (s *Store) Read(q Query) (*Answer, error) {
	now := s.Now()
	if q.To.IsZero() || q.To.After(now) {
		q.To = now
	}
	if q.From.IsZero() {
		q.From = q.To.Add(-time.Hour)
	}
	if !q.From.Before(q.To) {
		return nil, fmt.Errorf("from 要早于 to")
	}
	all, err := s.fields(q.Company, q.Plant, q.Asset)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(all))
	for f := range all {
		names = append(names, f)
	}
	sort.Strings(names)
	fields := q.Fields
	if len(fields) == 0 {
		fields = names
	}
	from, to := q.From.UnixMilli(), q.To.UnixMilli()
	span := q.To.Sub(q.From)
	res := q.Step
	rawFrom := now.Add(-s.ret.Raw)
	minFrom := now.Add(-s.ret.Minute)
	switch res {
	case Raw, Minute, Hour:
	case "":
		switch {
		case span <= 6*time.Hour && !q.From.Before(rawFrom):
			res = Raw
		case span <= 8*24*time.Hour && !q.From.Before(minFrom):
			res = Minute
		default:
			res = Hour
		}
	default:
		return nil, fmt.Errorf("step 只能是 raw、minute 或 hour")
	}
	auto := q.Step == ""
	a := &Answer{Company: q.Company, Plant: q.Plant, Asset: q.Asset, From: from, To: to, Resolution: res, Series: []Field{}, Events: []Event{}, Fields: names}
	for _, f := range fields {
		sid, ok := all[f]
		if !ok {
			a.Series = append(a.Series, Field{Field: f, Points: [][]any{}})
			continue
		}
		var pts [][]any
		if res == Raw {
			ps, err := s.raw(sid, from, to)
			if err != nil {
				return nil, err
			}
			if len(ps) > MaxRaw {
				if auto {
					return s.Read(Query{Company: q.Company, Plant: q.Plant, Asset: q.Asset, Fields: q.Fields, From: q.From, To: q.To, Step: Minute})
				}
				ps = ps[len(ps)-MaxRaw:]
				a.Truncated = true
			}
			pts = make([][]any, len(ps))
			for i, p := range ps {
				pts[i] = []any{p.T, p.V, p.Q}
			}
		} else {
			as, err := s.aggs(sid, res, from, to)
			if err != nil {
				return nil, err
			}
			pts = make([][]any, len(as))
			for i, x := range as {
				if x.N == 0 {
					pts[i] = []any{x.T, nil, nil, nil, 0, x.Flagged}
					continue
				}
				pts[i] = []any{x.T, round(x.Avg()), x.Min, x.Max, x.N, x.Flagged}
			}
		}
		a.Series = append(a.Series, Field{Field: f, Points: pts})
		evs, err := s.events(sid, f, from, to)
		if err != nil {
			return nil, err
		}
		a.Events = append(a.Events, evs...)
	}
	sort.Slice(a.Events, func(i, j int) bool { return a.Events[i].T < a.Events[j].T })
	return a, nil
}

func round(x float64) float64 { return math.Round(x*1e6) / 1e6 }

// fields are the series stored for an asset, by field.
func (s *Store) fields(c, p, asset string) (map[string]int64, error) {
	rows, err := s.db.Query(`SELECT field, id FROM series WHERE company=? AND plant=? AND asset=?`, c, p, asset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var f string
		var id int64
		rows.Scan(&f, &id)
		out[f] = id
	}
	return out, rows.Err()
}

func (s *Store) tail(sid, from, to int64) ([]Point, error) {
	rows, err := s.db.Query(`SELECT t, v, q, src FROM tail WHERE sid=? AND t >= ? AND t <= ? ORDER BY t`, sid, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Point
	for rows.Next() {
		var p Point
		rows.Scan(&p.T, &p.V, &p.Q, &p.Src)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) raw(sid, from, to int64) ([]Point, error) {
	rows, err := s.db.Query(`SELECT hour, data FROM raw WHERE sid=? AND hour >= ? AND hour <= ? ORDER BY hour`, sid, from/hour*hour, to)
	if err != nil {
		return nil, err
	}
	var sealed []Point
	for rows.Next() {
		var h int64
		var data []byte
		rows.Scan(&h, &data)
		ps, err := decodePoints(h, data)
		if err != nil {
			rows.Close()
			return nil, err
		}
		for _, p := range ps {
			if p.T >= from && p.T <= to {
				sealed = append(sealed, p)
			}
		}
	}
	rows.Close()
	tail, err := s.tail(sid, from, to)
	if err != nil {
		return nil, err
	}
	return mergePoints(sealed, tail), nil
}

// aggs are a series' minutes or hours over a range: sealed ones, and those of the tail worked
// out now (combined where an interval has both, i.e. readings that arrived after it was sealed).
func (s *Store) aggs(sid int64, res string, from, to int64) ([]Agg, error) {
	width, table, col, block := minute, "minutes", "hour", hour
	if res == Hour {
		width, table, col, block = hour, "hours", "day", day
	}
	rows, err := s.db.Query(`SELECT `+col+`, data FROM `+table+` WHERE sid=? AND `+col+` >= ? AND `+col+` <= ? ORDER BY `+col, sid, from/block*block, to)
	if err != nil {
		return nil, err
	}
	var out []Agg
	for rows.Next() {
		var b int64
		var data []byte
		rows.Scan(&b, &data)
		as, err := decodeAggs(b, width, data)
		if err != nil {
			rows.Close()
			return nil, err
		}
		for _, a := range as {
			if a.T+width > from && a.T <= to {
				out = append(out, a)
			}
		}
	}
	rows.Close()
	tail, err := s.tail(sid, from/width*width, to)
	if err != nil {
		return nil, err
	}
	for _, a := range aggregate(0, width, tail, tags.Doubtful) {
		if i := indexAgg(out, a.T); i >= 0 {
			out[i] = combine(a.T, []Agg{out[i], a})
			continue
		}
		out = putAgg(out, a)
	}
	return out, nil
}

func (s *Store) events(sid int64, field string, from, to int64) ([]Event, error) {
	rows, err := s.db.Query(`SELECT t, v, q, src FROM events WHERE sid=? AND t >= ? AND t <= ? ORDER BY t DESC LIMIT 2000`, sid, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Event
	for rows.Next() {
		var e Event
		var src uint32
		rows.Scan(&e.T, &e.V, &e.Q, &src)
		e.Field = field
		s.mu.Lock()
		e.Src = s.srcName[src]
		s.mu.Unlock()
		out = append(out, e)
	}
	return out, rows.Err()
}

// Assets are the assets of a company that have stored readings, as plant/asset.
func (s *Store) Assets(company string) ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT plant || '/' || asset FROM series WHERE company=? ORDER BY 1`, company)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		rows.Scan(&a)
		out = append(out, a)
	}
	return out, rows.Err()
}
