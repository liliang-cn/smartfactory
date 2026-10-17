package tables

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	"smartfactory/internal/engine"
)

// TableNow is a table as the console and the tools show it: the twin's state, and who last reported it.
type TableNow struct {
	engine.Table
	// LastSource and LastAt are the newest report or session event ("" and nil when nothing has reported it).
	LastSource string `json:"lastSource,omitempty"`
	LastAt     *int64 `json:"lastAt,omitempty"`
	// Minutes the current state has lasted.
	Minutes float64 `json:"minutes"`
	// Sessions today at this table.
	Sessions int `json:"sessions"`
}

// KPIs are a restaurant's (or one plant's) dining figures: now, and over the business day so far.
type KPIs struct {
	Plant string `json:"plant,omitempty"`
	Name  string `json:"name,omitempty"`
	// Day is the business day (from 05:00) the figures are of.
	Day      string `json:"day"`
	Tables   int    `json:"tables"`
	Seats    int    `json:"seats"`
	Occupied int    `json:"occupied"`
	Reserved int    `json:"reserved"`
	Cleaning int    `json:"cleaning"`
	Free     int    `json:"free"`
	People   int    `json:"people"`
	// Occupancy 上座率: occupied tables / tables, now.
	Occupancy float64 `json:"occupancy"`
	// SeatUse 座位利用率: people seated / seats, now.
	SeatUse float64 `json:"seatUse"`
	// Sessions started today; Turnover 翻台率: sessions per table today.
	Sessions int     `json:"sessions"`
	Turnover float64 `json:"turnover"`
	// AvgMinutes 平均用餐时长 of the sessions that ended today (nil when none has).
	AvgMinutes *float64 `json:"avgMinutes"`
	// Guests today, in sessions that started today.
	Guests int `json:"guests"`
	// Waiting groups now (0 when the twin does not know).
	Waiting int `json:"waiting"`
	// Source of the figures: measured (every table reported), simulated (none), mixed;
	// MeasuredShare is the share of tables reported now, ReportedSessions today's from reports.
	Source           string  `json:"source"`
	MeasuredShare    float64 `json:"measuredShare"`
	ReportedSessions int     `json:"reportedSessions"`
}

// Overview is a company's tables and figures now.
type Overview struct {
	Tables []TableNow `json:"tables"`
	Total  KPIs       `json:"total"`
	Plants []KPIs     `json:"plants"`
	Now    int64      `json:"now"`
}

func sourceOf(share float64) string {
	switch {
	case share >= 0.999:
		return "measured"
	case share <= 0.001:
		return "simulated"
	}
	return "mixed"
}

func r3(x float64) float64 { return math.Round(x*1000) / 1000 }

// Overview is the company's tables now, with today's figures per plant and in all.
func (s *Service) Overview(company string) (*Overview, error) {
	now := s.Now()
	from := DayStart(now)
	list := s.Twin.TableList(company)
	today, err := s.Between(company, "", "", from, now.Add(time.Second))
	if err != nil {
		return nil, err
	}
	perTable := map[string]int{}
	for _, x := range today {
		if x.Start >= from.UnixMilli() {
			perTable[x.Plant+"/"+x.Table]++
		}
	}
	out := &Overview{Tables: []TableNow{}, Plants: []KPIs{}, Now: now.UnixMilli()}
	for _, t := range list {
		tn := TableNow{Table: t, Sessions: perTable[t.Plant+"/"+t.ID]}
		if t.Since > 0 {
			tn.Minutes = math.Round(float64(max(0, now.UnixMilli()-t.Since))/6000) / 10
		}
		src, at, err := s.lastReport(company, t.Plant, t.ID)
		if err != nil {
			return nil, err
		}
		if src != "" {
			ms := at.UnixMilli()
			tn.LastSource, tn.LastAt = src, &ms
		}
		out.Tables = append(out.Tables, tn)
	}
	byPlant := map[string][]engine.Table{}
	names := map[string]string{}
	var order []string
	for _, t := range list {
		if _, ok := byPlant[t.Plant]; !ok {
			order = append(order, t.Plant)
		}
		byPlant[t.Plant] = append(byPlant[t.Plant], t)
		names[t.Plant] = t.PlantName
	}
	for _, p := range order {
		k := figures(byPlant[p], today, from, now)
		k.Plant, k.Name = p, names[p]
		out.Plants = append(out.Plants, k)
	}
	out.Total = figures(list, today, from, now)
	return out, nil
}

// figures are the KPIs of these tables from their state now and the sessions of the day.
func figures(list []engine.Table, sessions []Session, from, now time.Time) KPIs {
	k := KPIs{Day: from.Format("2006-01-02")}
	in := map[string]bool{}
	reported := 0
	waiting := map[string]int{}
	for _, t := range list {
		if t.Waiting > waiting[t.Plant] {
			k.Waiting += t.Waiting - waiting[t.Plant]
			waiting[t.Plant] = t.Waiting
		}
		in[t.Plant+"/"+t.ID] = true
		k.Tables++
		k.Seats += t.Seats
		switch t.State {
		case Occupied:
			k.Occupied++
			k.People += t.People
		case Reserved:
			k.Reserved++
		case Cleaning:
			k.Cleaning++
		default:
			k.Free++
		}
		if t.Reported {
			reported++
		}
	}
	var mins float64
	ended := 0
	for _, x := range sessions {
		if !in[x.Plant+"/"+x.Table] {
			continue
		}
		if x.Start >= from.UnixMilli() {
			k.Sessions++
			k.Guests += x.People
			if x.Source != Sim {
				k.ReportedSessions++
			}
		}
		if x.End != nil && *x.End >= from.UnixMilli() && *x.End <= now.UnixMilli() {
			mins += x.Minutes(now)
			ended++
		}
	}
	if k.Tables > 0 {
		k.Occupancy = r3(float64(k.Occupied) / float64(k.Tables))
		k.Turnover = r3(float64(k.Sessions) / float64(k.Tables))
		k.MeasuredShare = r3(float64(reported) / float64(k.Tables))
	}
	if k.Seats > 0 {
		k.SeatUse = r3(float64(k.People) / float64(k.Seats))
	}
	if ended > 0 {
		a := math.Round(mins/float64(ended)*10) / 10
		k.AvgMinutes = &a
	}
	k.Source = sourceOf(k.MeasuredShare)
	return k
}

// Bucket is an hour or a day of a restaurant's sessions.
type Bucket struct {
	Plant string `json:"plant"`
	// Start of the hour or the business day (ms), and as local time.
	Start int64  `json:"start"`
	Label string `json:"label"`
	// Sessions that started in it, their guests, the mean length of those that have ended.
	Sessions   int      `json:"sessions"`
	Guests     int      `json:"guests"`
	AvgMinutes *float64 `json:"avgMinutes"`
	// OccupiedHours: table-hours taken within it (every session's overlap with it).
	OccupiedHours float64 `json:"occupiedHours"`
	// Reported sessions among them.
	Reported int `json:"reported"`
}

// History is the company's sessions by hour or by business day over [from, to), per plant.
func (s *Service) History(company string, from, to time.Time, byDay bool) ([]Bucket, error) {
	step := time.Hour
	start := from.Truncate(time.Hour)
	if byDay {
		step = 24 * time.Hour
		start = DayStart(from)
	}
	if !to.After(start) {
		return []Bucket{}, nil
	}
	if to.Sub(start) > 400*24*time.Hour {
		to = start.Add(400 * 24 * time.Hour)
	}
	list, err := s.Between(company, "", "", start, to)
	if err != nil {
		return nil, err
	}
	now := s.Now()
	type key struct {
		plant string
		at    int64
	}
	acc := map[key]*Bucket{}
	mins := map[key]float64{}
	ended := map[key]int{}
	get := func(k key) *Bucket {
		b := acc[k]
		if b == nil {
			b = &Bucket{Plant: k.plant, Start: k.at}
			if byDay {
				b.Label = time.UnixMilli(k.at).In(shanghai).Format("2006-01-02")
			} else {
				b.Label = time.UnixMilli(k.at).In(shanghai).Format("2006-01-02 15:00")
			}
			acc[k] = b
		}
		return b
	}
	bucketOf := func(ms int64) int64 {
		t := time.UnixMilli(ms)
		if byDay {
			return DayStart(t).UnixMilli()
		}
		return t.Truncate(time.Hour).UnixMilli()
	}
	for _, x := range list {
		end := now.UnixMilli()
		if x.End != nil {
			end = *x.End
		}
		if x.Start >= start.UnixMilli() && x.Start < to.UnixMilli() {
			k := key{x.Plant, bucketOf(x.Start)}
			b := get(k)
			b.Sessions++
			b.Guests += x.People
			if x.Source != Sim {
				b.Reported++
			}
			if x.End != nil {
				mins[k] += x.Minutes(now)
				ended[k]++
			}
		}
		// the hours (or days) the session spans
		for at := max(bucketOf(x.Start), start.UnixMilli()); at < min(end, to.UnixMilli()); {
			next := time.UnixMilli(at).Add(step).UnixMilli()
			if byDay {
				next = DayStart(time.UnixMilli(at)).AddDate(0, 0, 1).UnixMilli()
			}
			lo, hi := max(at, x.Start), min(next, end)
			if hi > lo {
				get(key{x.Plant, at}).OccupiedHours += float64(hi-lo) / 3_600_000
			}
			at = next
		}
	}
	out := make([]Bucket, 0, len(acc))
	for k, b := range acc {
		b.OccupiedHours = math.Round(b.OccupiedHours*100) / 100
		if n := ended[k]; n > 0 {
			a := math.Round(mins[k]/float64(n)*10) / 10
			b.AvgMinutes = &a
		}
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Start != out[j].Start {
			return out[i].Start < out[j].Start
		}
		return out[i].Plant < out[j].Plant
	})
	return out, nil
}

// Current is a table's state now and its session in progress, with today's sessions: "A03 坐了多久".
type Current struct {
	Table   TableNow  `json:"table"`
	Open    *Session  `json:"open"`
	Minutes *float64  `json:"minutes"`
	Today   []Session `json:"today"`
}

// Table finds one table (its plant may be left out) with its session now and today's.
func (s *Service) Table(company, plant, table string) (*Current, error) {
	ov, err := s.Overview(company)
	if err != nil {
		return nil, err
	}
	var hit *TableNow
	for i, t := range ov.Tables {
		if t.ID == table && (plant == "" || t.Plant == plant) {
			if hit != nil {
				return nil, ErrAmbiguous
			}
			hit = &ov.Tables[i]
		}
	}
	if hit == nil {
		return nil, ErrNoTable
	}
	today, err := s.Day(company, hit.Plant, hit.ID, time.Time{})
	if err != nil {
		return nil, err
	}
	c := &Current{Table: *hit, Today: today}
	open, err := s.openSession(company, hit.Plant, hit.ID)
	if err != nil {
		return nil, err
	}
	if open == nil && hit.State == Occupied {
		// the twin seated them and has not said so yet
		open = &Session{Plant: hit.Plant, Table: hit.ID, Start: hit.Since, People: hit.People, Source: hit.Source}
	}
	if open != nil && hit.State == Occupied {
		m := math.Round(open.Minutes(s.Now())*10) / 10
		c.Open, c.Minutes = open, &m
	}
	return c, nil
}

// Range reads a span of business days: from and to are YYYY-MM-DD (to included), RFC 3339 or
// ms; from defaults to six days before today, to to now.
func Range(now time.Time, from, to string) (time.Time, time.Time, error) {
	parse := func(s string, def time.Time) (time.Time, bool, error) {
		if s == "" {
			return def, false, nil
		}
		if t, err := time.ParseInLocation("2006-01-02", s, shanghai); err == nil {
			return t.Add(5 * time.Hour), true, nil
		}
		if t, err := time.Parse(time.RFC3339, s); err == nil {
			return t, false, nil
		}
		if ms, err := strconv.ParseInt(s, 10, 64); err == nil {
			return time.UnixMilli(ms), false, nil
		}
		return time.Time{}, false, fmt.Errorf("时间 %s 看不懂：用 YYYY-MM-DD、RFC 3339 或毫秒时间戳", s)
	}
	f, _, err := parse(from, DayStart(now).AddDate(0, 0, -6))
	if err != nil {
		return f, f, err
	}
	t, day, err := parse(to, now)
	if err != nil {
		return f, t, err
	}
	if day {
		t = t.AddDate(0, 0, 1)
	}
	return f, t, nil
}
