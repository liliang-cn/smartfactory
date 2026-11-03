package plan

import (
	"slices"
	"sort"
)

// A line's working time: the stretches it runs and at what pace (1 = full, a shift handover a
// half), from the plant's calendar — shifts, meal breaks, opening hours, days off, its weekly
// planned maintenance — and the stops planned for it. Times are Unix seconds; the plant's day is
// China time (UTC+8), as the twin's clock is.

const day = 86400

// local is the offset of the plant's wall clock from UTC.
const local = 8 * 3600

// Calendar is the working week of a plant.
type Calendar struct {
	// Breaks are meal breaks: the crewed lines stop. At is the hour of the day (11.5 = 11:30).
	Breaks []Break `json:"breaks,omitempty"`
	// Shifts are the hours shifts change; for Handover minutes crews work at Pace.
	Shifts   []float64 `json:"shifts,omitempty"`
	Handover float64   `json:"handover,omitempty"`
	Pace     float64   `json:"pace,omitempty"`
	// Open are the opening hours (nil: round the clock).
	Open *[2]float64 `json:"open,omitempty"`
	// Off are the weekdays the plant does not run (Sunday 0).
	Off []int `json:"off,omitempty"`
	// PM is the weekly planned maintenance: Hours long from hour Start, each line on its own
	// weekday (line n on weekday (n-1) mod 7), as the twin does it.
	PM *[2]float64 `json:"pm,omitempty"`
}

// Break is a meal break.
type Break struct {
	At      float64 `json:"at"`
	Minutes float64 `json:"minutes"`
	Label   string  `json:"label,omitempty"`
}

// Stop is a stretch a line is planned not to run (maintenance, a repair, a trial).
type Stop struct {
	Line   string `json:"line"`
	From   int64  `json:"from"`
	To     int64  `json:"to"`
	Reason string `json:"reason,omitempty"`
}

type seg struct {
	from, to int64
	rate     float64
}

// timeline is when a line works and how fast, from `from` to `to`; after `to` it works on at
// full pace (a plan that long is a sketch anyway).
type timeline struct {
	segs     []seg
	from, to int64
}

type mark struct {
	at   int64
	stop int // +1 a stop starts, -1 one ends
	slow int
	pace float64
}

// build lays out a line's working time from `from` for `days` days. crew: the line stops for
// meal breaks and slows for handovers (an unmanned cell does not); no is the line's number in
// its plant (its maintenance weekday).
func build(c *Calendar, crew bool, no int, stops []Stop, from int64, days int) *timeline {
	if c == nil {
		c = &Calendar{}
	}
	start := (from+local)/day*day - local // midnight, plant time, of the first day
	end := start + int64(days+1)*day
	var ms []mark
	stop := func(a, b int64) {
		if b > a {
			ms = append(ms, mark{at: a, stop: 1}, mark{at: b, stop: -1})
		}
	}
	slow := func(a, b int64, pace float64) {
		if b > a {
			ms = append(ms, mark{at: a, slow: 1, pace: pace}, mark{at: b, slow: -1, pace: pace})
		}
	}
	hour := func(d int64, h float64) int64 { return d + int64(h*3600) }
	for d := start; d < end; d += day {
		wd := int(((d+local)/day + 4) % 7) // 1970-01-01 was a Thursday
		if slices.Contains(c.Off, wd) {
			stop(d, d+day)
			continue
		}
		if o := c.Open; o != nil && int(o[0])%24 != int(o[1])%24 {
			if o[0] < o[1] {
				stop(d, hour(d, o[0]))
				stop(hour(d, o[1]), d+day)
			} else {
				stop(hour(d, o[1]), hour(d, o[0]))
			}
		}
		if crew {
			for _, b := range c.Breaks {
				a := hour(d, b.At)
				stop(a, a+int64(b.Minutes*60))
			}
			if c.Handover > 0 && c.Pace < 1 {
				for _, h := range c.Shifts {
					a := hour(d, h)
					slow(a, a+int64(c.Handover*60), c.Pace)
				}
			}
		}
		if pm := c.PM; pm != nil && no > 0 && (no-1)%7 == wd {
			a := hour(d, pm[1])
			stop(a, a+int64(pm[0]*3600))
		}
	}
	for _, s := range stops {
		stop(max(s.From, start), min(s.To, end))
	}
	sort.SliceStable(ms, func(i, j int) bool { return ms[i].at < ms[j].at })
	t := &timeline{from: start, to: end}
	nstop, slows := 0, 0
	pace := 1.0
	at := start
	emit := func(b int64) {
		if b <= at {
			return
		}
		if nstop == 0 {
			r := 1.0
			if slows > 0 {
				r = pace
			}
			if n := len(t.segs); n > 0 && t.segs[n-1].to == at && t.segs[n-1].rate == r {
				t.segs[n-1].to = b
			} else {
				t.segs = append(t.segs, seg{at, b, r})
			}
		}
		at = b
	}
	for _, m := range ms {
		emit(min(max(m.at, start), end))
		nstop += m.stop
		slows += m.slow
		if m.slow > 0 {
			pace = m.pace
		}
		if slows == 0 {
			pace = 1
		}
	}
	emit(end)
	return t
}

// find is the index of the first stretch that ends after t.
func (t *timeline) find(at int64) int {
	return sort.Search(len(t.segs), func(i int) bool { return t.segs[i].to > at })
}

// next is the first moment at or after at the line works.
func (t *timeline) next(at int64) int64 {
	if at >= t.to {
		return at
	}
	i := t.find(at)
	if i >= len(t.segs) {
		return t.to
	}
	return max(at, t.segs[i].from)
}

// advance is when work seconds of full-pace work done from at are finished.
func (t *timeline) advance(at int64, work float64) int64 {
	if work <= 0 {
		return at
	}
	if at < t.to {
		for i := t.find(at); i < len(t.segs); i++ {
			s := t.segs[i]
			a := max(at, s.from)
			cap := float64(s.to-a) * s.rate
			if cap >= work {
				return a + int64(work/s.rate+0.999)
			}
			work -= cap
			at = s.to
		}
		at = max(at, t.to)
	}
	return at + int64(work+0.999)
}

// work is the full-pace seconds the line can work between a and b.
func (t *timeline) work(a, b int64) float64 {
	var w float64
	if a < t.to {
		for i := t.find(a); i < len(t.segs) && t.segs[i].from < b; i++ {
			s := t.segs[i]
			w += float64(min(b, s.to)-max(a, s.from)) * s.rate
		}
	}
	if b > t.to {
		w += float64(b - max(a, t.to))
	}
	return w
}
