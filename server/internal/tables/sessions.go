package tables

import (
	"database/sql"
	"sort"
	"time"
)

// Session is one group at one table: from when they sat down to when they left (nil while
// they are still there), how many, and what reported it ("sim" for the twin's own).
type Session struct {
	Plant  string `json:"plant"`
	Table  string `json:"table"`
	Start  int64  `json:"start"`
	End    *int64 `json:"end"`
	People int    `json:"people"`
	Source string `json:"source"`
}

// Minutes the session has lasted (up to now while it is open).
func (x Session) Minutes(now time.Time) float64 {
	end := now.UnixMilli()
	if x.End != nil {
		end = *x.End
	}
	return float64(max(0, end-x.Start)) / 60000
}

type mark struct {
	at     int64
	kind   string // a state or an event
	people int
	source string
}

// rebuild works out the table's reported sessions again from the business day before `from`
// on, so a late report lands where it belongs. The twin's own sessions are left alone.
func (s *Service) rebuild(company, plant, table string, from time.Time) error {
	start := DayStart(from).AddDate(0, 0, -1).UnixMilli()
	var marks []mark
	rows, err := s.db.Query(`SELECT since, state, people, source FROM reports WHERE company=? AND plant=? AND tbl=? AND since >= ?
		UNION ALL SELECT at, event, people, source FROM events WHERE company=? AND plant=? AND tbl=? AND at >= ?`,
		company, plant, table, start, company, plant, table, start)
	if err != nil {
		return err
	}
	for rows.Next() {
		var m mark
		if err := rows.Scan(&m.at, &m.kind, &m.people, &m.source); err != nil {
			rows.Close()
			return err
		}
		marks = append(marks, m)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	// in time order; at the same moment a close before an open, so "left" then "seated" is two sessions
	rank := map[string]int{Free: 0, Cleaning: 0, Paid: 0, Left: 0, Reserved: 1, Ordered: 2, Occupied: 3, Seated: 3}
	sort.SliceStable(marks, func(i, j int) bool {
		if marks[i].at != marks[j].at {
			return marks[i].at < marks[j].at
		}
		return rank[marks[i].kind] < rank[marks[j].kind]
	})

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// a reported session already open when the window starts carries on
	var open *Session
	var o Session
	var end sql.NullInt64
	err = tx.QueryRow(`SELECT start, end, people, source FROM sessions WHERE company=? AND plant=? AND tbl=? AND source<>? AND start < ?
		AND (end IS NULL OR end >= ?) ORDER BY start DESC LIMIT 1`, company, plant, table, Sim, start, start).Scan(&o.Start, &end, &o.People, &o.Source)
	if err == nil {
		o.Plant, o.Table = plant, table
		open = &o
	} else if err != sql.ErrNoRows {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM sessions WHERE company=? AND plant=? AND tbl=? AND source<>? AND start >= ?`, company, plant, table, Sim, start); err != nil {
		return err
	}
	var out []Session
	if open != nil {
		open.End = nil
	}
	for _, m := range marks {
		switch m.kind {
		case Occupied, Seated, Ordered:
			if open != nil {
				if m.people > 0 {
					open.People = m.people
				}
				continue
			}
			p := m.people
			if p == 0 {
				p = 1
			}
			open = &Session{Plant: plant, Table: table, Start: m.at, People: p, Source: m.source}
		case Free, Cleaning, Paid, Left, Reserved:
			if open != nil {
				at := m.at
				open.End = &at
				out = append(out, *open)
				open = nil
			}
		}
	}
	if open != nil {
		out = append(out, *open)
	}
	for _, x := range out {
		var e any
		if x.End != nil {
			e = *x.End
		}
		// a reported session takes the place of the twin's at the same moment
		if _, err := tx.Exec(`INSERT INTO sessions VALUES (?,?,?,?,?,?,?) ON CONFLICT DO UPDATE SET end=excluded.end, people=excluded.people, source=excluded.source`,
			company, plant, table, x.Start, e, x.People, x.Source); err != nil {
			return err
		}
	}
	// the twin's sessions that overlap a reported one were the twin's guess: the report wins
	for _, x := range out {
		e := int64(1 << 62)
		if x.End != nil {
			e = *x.End
		}
		if _, err := tx.Exec(`DELETE FROM sessions WHERE company=? AND plant=? AND tbl=? AND source=? AND start < ? AND (end IS NULL OR end > ?)`,
			company, plant, table, Sim, e, x.Start); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// openSession is the table's session in progress (reported or simulated), nil when there is none.
func (s *Service) openSession(company, plant, table string) (*Session, error) {
	var x Session
	err := s.db.QueryRow(`SELECT start, people, source FROM sessions WHERE company=? AND plant=? AND tbl=? AND end IS NULL ORDER BY start DESC LIMIT 1`,
		company, plant, table).Scan(&x.Start, &x.People, &x.Source)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	x.Plant, x.Table = plant, table
	return &x, nil
}

// Between is the company's sessions that overlap [from, to), oldest first; plant and table
// narrow it ("" for all).
func (s *Service) Between(company, plant, table string, from, to time.Time) ([]Session, error) {
	q := `SELECT plant, tbl, start, end, people, source FROM sessions WHERE company=? AND start < ? AND (end IS NULL OR end > ?)`
	args := []any{company, to.UnixMilli(), from.UnixMilli()}
	if plant != "" {
		q += ` AND plant=?`
		args = append(args, plant)
	}
	if table != "" {
		q += ` AND tbl=?`
		args = append(args, table)
	}
	rows, err := s.db.Query(q+` ORDER BY start`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		var x Session
		var end sql.NullInt64
		if err := rows.Scan(&x.Plant, &x.Table, &x.Start, &end, &x.People, &x.Source); err != nil {
			return nil, err
		}
		if end.Valid {
			e := end.Int64
			x.End = &e
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// Day is a table's sessions on the business day `day` falls in (zero: today).
func (s *Service) Day(company, plant, table string, day time.Time) ([]Session, error) {
	if day.IsZero() {
		day = s.Now()
	}
	from := DayStart(day)
	return s.Between(company, plant, table, from, from.AddDate(0, 0, 1))
}

// Mark is one report or session event as it was kept.
type Mark struct {
	At       int64  `json:"at"`
	Kind     string `json:"kind"`
	People   int    `json:"people"`
	Source   string `json:"source"`
	Received int64  `json:"received"`
}

// Marks are a table's reports and session events since from, newest first (at most limit).
func (s *Service) Marks(company, plant, table string, from time.Time, limit int) ([]Mark, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT since, state, people, source, received FROM reports WHERE company=? AND plant=? AND tbl=? AND since >= ?
		UNION ALL SELECT at, event, people, source, received FROM events WHERE company=? AND plant=? AND tbl=? AND at >= ?
		ORDER BY 1 DESC LIMIT ?`, company, plant, table, from.UnixMilli(), company, plant, table, from.UnixMilli(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Mark{}
	for rows.Next() {
		var m Mark
		if err := rows.Scan(&m.At, &m.Kind, &m.People, &m.Source, &m.Received); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
