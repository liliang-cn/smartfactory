package checks

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"time"

	"smartfactory/internal/engine"
)

// Round is a form due on one machine: this round's window, and whether it was done.
type Round struct {
	Form      string `json:"form"`
	FormName  string `json:"formName"`
	Kind      string `json:"kind"`
	Plant     string `json:"plant"`
	PlantName string `json:"plantName"`
	Line      string `json:"line"`
	LineName  string `json:"lineName"`
	Asset     string `json:"asset"`
	AssetName string `json:"assetName"`
	Every     string `json:"every"`
	// Status: done (this round is in) | due (to do, still in time) | overdue (past its time, or
	// the last round was missed) | open (no schedule: filled in when needed)
	Status string `json:"status"`
	// Start is when this round started, Due when it must be done by, Next when the next starts.
	Start *time.Time `json:"start,omitempty"`
	Due   *time.Time `json:"due,omitempty"`
	Next  *time.Time `json:"next,omitempty"`
	// Since is when it fell overdue.
	Since *time.Time `json:"since,omitempty"`
	// Last is when it was last filled in, by whom, and how many values were abnormal.
	Last    *time.Time `json:"last,omitempty"`
	LastBy  string     `json:"lastBy,omitempty"`
	LastBad int        `json:"lastBad,omitempty"`
}

// Machine is a station some form is for, with its rounds.
type Machine struct {
	Plant     string       `json:"plant"`
	PlantName string       `json:"plantName"`
	Line      string       `json:"line"`
	LineName  string       `json:"lineName"`
	Asset     string       `json:"asset"`
	Name      string       `json:"name"`
	Label     string       `json:"label"`
	Measure   *engine.Spec `json:"measure,omitempty"`
	Rounds    []Round      `json:"rounds"`
}

// starts are the times rounds of sc start from the day before yesterday to the day after
// tomorrow around now, in order.
func (sc Schedule) starts(now time.Time, loc *time.Location) []time.Time {
	at := sc.At
	switch {
	case len(at) > 0:
	case sc.Every == "shift":
		at = []string{"08:00", "20:00"}
	case sc.Every == "day":
		at = []string{"08:00"}
	default:
		at = []string{"00:00"}
	}
	clock := func(day time.Time, hhmm string) time.Time {
		h, _ := strconv.Atoi(hhmm[:2])
		m, _ := strconv.Atoi(hhmm[3:])
		return time.Date(day.Year(), day.Month(), day.Day(), h, m, 0, 0, loc)
	}
	local := now.In(loc)
	var out []time.Time
	for d := -2; d <= 2; d++ {
		day := local.AddDate(0, 0, d)
		if sc.Every == "hours" {
			// rounds every so many hours from the day's first, starting over the next day
			first := clock(day, at[0])
			for t := first; t.Before(first.AddDate(0, 0, 1)); t = t.Add(time.Duration(sc.Hours) * time.Hour) {
				out = append(out, t)
			}
			continue
		}
		for _, a := range at {
			out = append(out, clock(day, a))
		}
	}
	slices.SortFunc(out, func(a, b time.Time) int { return a.Compare(b) })
	return slices.CompactFunc(out, func(a, b time.Time) bool { return a.Equal(b) })
}

// window is the round now falls in, [start, end), and the one before it from prev.
func (sc Schedule) window(now time.Time, loc *time.Location) (prev, start, end time.Time) {
	ts := sc.starts(now, loc)
	i, _ := slices.BinarySearchFunc(ts, now, func(t, n time.Time) int {
		if t.After(n) {
			return 1
		}
		return -1
	})
	// ts[i-1] is the last start at or before now
	return ts[i-2], ts[i-1], ts[i]
}

// deadline is when a round that started at start must be done by: Within minutes from it, but
// never past the next round's start.
func (sc Schedule) deadline(start, end time.Time) time.Time {
	if sc.Within > 0 {
		if d := start.Add(time.Duration(sc.Within) * time.Minute); d.Before(end) {
			return d
		}
	}
	return end
}

// round is where form f stands on a machine last filled in at last (nil: never).
func (s *Service) round(f *Form, last *time.Time) Round {
	r := Round{Form: f.ID, FormName: f.Name, Kind: f.Kind, Every: f.Schedule.Every, Status: "open", Last: last}
	if f.Schedule.Every == "" {
		return r
	}
	now := s.Now()
	prev, start, end := f.Schedule.window(now, s.Loc)
	due := f.Schedule.deadline(start, end)
	r.Start, r.Due, r.Next = &start, &due, &end
	switch {
	case last != nil && !last.Before(start):
		r.Status = "done"
	case !now.Before(due):
		r.Status, r.Since = "overdue", &due
	case !f.Created.After(prev) && (last == nil || last.Before(prev)):
		// the last round went by without anyone filling it in (the form was there all of it)
		missed := f.Schedule.deadline(prev, start)
		r.Status, r.Since = "overdue", &missed
	default:
		r.Status = "due"
	}
	return r
}

type lastFill struct {
	at  time.Time
	by  string
	bad int
}

// lasts is when each form was last filled in on each machine: form/plant/asset → when, who.
func (s *Service) lasts(company string) (map[string]lastFill, error) {
	rows, err := s.db.Query(`SELECT form, plant, asset, max(taken), who, bad FROM submissions WHERE company=? GROUP BY form, plant, asset`, company)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]lastFill{}
	for rows.Next() {
		var f, p, a, who string
		var at int64
		var bad int
		if rows.Scan(&f, &p, &a, &at, &who, &bad) == nil {
			out[f+"/"+p+"/"+a] = lastFill{time.UnixMilli(at), who, bad}
		}
	}
	return out, rows.Err()
}

// Machines are the company's stations some form is for, each with its rounds, by plant and line.
func (s *Service) Machines(company string) ([]Machine, error) {
	forms, err := s.Forms(company)
	if err != nil {
		return nil, err
	}
	lasts, err := s.lasts(company)
	if err != nil {
		return nil, err
	}
	out := []Machine{}
	for _, p := range s.Eng.Stations(company) {
		for _, l := range p.Lines {
			for _, st := range l.Stations {
				m := Machine{Plant: p.ID, PlantName: p.Name, Line: l.ID, LineName: l.Name, Asset: st.ID, Name: st.Name, Label: st.Label, Measure: st.Measure, Rounds: []Round{}}
				for i := range forms {
					f := &forms[i]
					if !f.applies(p.ID, l.ID, st.ID) {
						continue
					}
					var last *time.Time
					lf, ok := lasts[f.ID+"/"+p.ID+"/"+st.ID]
					if ok {
						last = &lf.at
					}
					r := s.round(f, last)
					r.Plant, r.PlantName, r.Line, r.LineName, r.Asset, r.AssetName = p.ID, p.Name, l.ID, l.Name, st.ID, st.Name
					r.LastBy, r.LastBad = lf.by, lf.bad
					m.Rounds = append(m.Rounds, r)
				}
				if len(m.Rounds) > 0 {
					out = append(out, m)
				}
			}
		}
	}
	return out, nil
}

// Machine is one station with its rounds; false when no form is for it or there is no such station.
func (s *Service) Machine(company, plant, asset string) (*Machine, bool, error) {
	ms, err := s.Machines(company)
	if err != nil {
		return nil, false, err
	}
	for i := range ms {
		if ms[i].Plant == plant && ms[i].Asset == asset {
			return &ms[i], true, nil
		}
	}
	return nil, false, nil
}

// Rounds are the company's rounds with one of the statuses (none: all of them), overdue first and
// longest overdue first, then those due soonest.
func (s *Service) Rounds(company string, status ...string) ([]Round, error) {
	ms, err := s.Machines(company)
	if err != nil {
		return nil, err
	}
	out := []Round{}
	for _, m := range ms {
		for _, r := range m.Rounds {
			if len(status) == 0 || slices.Contains(status, r.Status) {
				out = append(out, r)
			}
		}
	}
	rank := map[string]int{"overdue": 0, "due": 1, "done": 2, "open": 3}
	at := func(t *time.Time) int64 {
		if t == nil {
			return 1 << 62
		}
		return t.UnixMilli()
	}
	slices.SortStableFunc(out, func(a, b Round) int {
		if rank[a.Status] != rank[b.Status] {
			return rank[a.Status] - rank[b.Status]
		}
		if a.Status == "overdue" {
			return cmp.Compare(at(a.Since), at(b.Since))
		}
		if c := cmp.Compare(at(a.Due), at(b.Due)); c != 0 {
			return c
		}
		return strings.Compare(a.Asset, b.Asset)
	})
	return out, nil
}
