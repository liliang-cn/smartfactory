package history

import (
	"cmp"
	"context"
	"slices"
	"sync"
	"time"
)

// Day is what a company's plants did on one local day, from one source (measured, simulated or
// mixed, as the rows' source column says): the lines' hours that make up OEE and output, and
// the machines' hours lost to faults, maintenance and die changes.
type Day struct {
	// Day is the local date, at midnight UTC (the database's wall clock carries no zone).
	Day    time.Time `json:"day"`
	Source string    `json:"source"`
	// LineHours is how many line-hour rows there were; PlannedS and GoodIdealS are OEE's
	// denominator and numerator (OEE = GoodIdealS / PlannedS).
	LineHours  int     `json:"lineHours"`
	PlannedS   float64 `json:"plannedS"`
	GoodIdealS float64 `json:"goodIdealS"`
	Produced   int     `json:"produced"`
	Good       int     `json:"good"`
	// MachineHours is how many machine-hour rows there were; DownS the seconds they spent in
	// a fault, maintenance or a die change.
	MachineHours int     `json:"machineHours"`
	DownS        float64 `json:"downS"`
}

// Days is the history at or after from and before to, by local day and source, oldest first.
func (s *Store) Days(ctx context.Context, from, to time.Time) ([]Day, error) {
	type key struct {
		day    time.Time
		source string
	}
	at := map[key]*Day{}
	var order []key
	get := func(day time.Time, source string) *Day {
		k := key{day, source}
		if d, ok := at[k]; ok {
			return d
		}
		d := &Day{Day: day, Source: source}
		at[k] = d
		order = append(order, k)
		return d
	}
	rows, err := s.db.Query(ctx, `SELECT date_trunc('day', hour), source, count(*), sum(planned_s), sum(good * ideal_cycle_s), sum(produced), sum(good)
		FROM line_hours WHERE hour >= $1 AND hour < $2 GROUP BY 1, 2`, wall(from), wall(to))
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var day time.Time
		var source string
		var x Day
		if err := rows.Scan(&day, &source, &x.LineHours, &x.PlannedS, &x.GoodIdealS, &x.Produced, &x.Good); err != nil {
			rows.Close()
			return nil, err
		}
		d := get(day, source)
		d.LineHours, d.PlannedS, d.GoodIdealS, d.Produced, d.Good = x.LineHours, x.PlannedS, x.GoodIdealS, x.Produced, x.Good
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows, err = s.db.Query(ctx, `SELECT date_trunc('day', hour), source, count(*), sum(fault_s + maint_s + changeover_s)
		FROM machine_hours WHERE hour >= $1 AND hour < $2 GROUP BY 1, 2`, wall(from), wall(to))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var day time.Time
		var source string
		var n int
		var down float64
		if err := rows.Scan(&day, &source, &n, &down); err != nil {
			return nil, err
		}
		d := get(day, source)
		d.MachineHours, d.DownS = n, down
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Day, 0, len(order))
	for _, k := range order {
		out = append(out, *at[k])
	}
	slices.SortFunc(out, func(a, b Day) int { return cmp.Or(a.Day.Compare(b.Day), cmp.Compare(a.Source, b.Source)) })
	return out, nil
}

// Reader reads one company's history for reports, opening the database the first time it is
// asked and again after a failure: a database that is down when the server starts is read once
// it is back, and nothing waits on it before.
type Reader struct {
	DSN string

	mu sync.Mutex
	st *Store
}

func (r *Reader) Days(ctx context.Context, from, to time.Time) ([]Day, error) {
	r.mu.Lock()
	if r.st == nil {
		st, err := Open(ctx, r.DSN)
		if err != nil {
			r.mu.Unlock()
			return nil, err
		}
		r.st = st
	}
	st := r.st
	r.mu.Unlock()
	return st.Days(ctx, from, to)
}

func (r *Reader) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.st != nil {
		r.st.Close()
		r.st = nil
	}
}
