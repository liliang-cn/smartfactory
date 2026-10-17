// Package tables keeps which tables of a restaurant are taken, from what reports them — a
// camera box, the POS, a table sensor, a waiter's phone — and from the twin where nothing does.
// A report is a table's state from a moment on (free, occupied, reserved, cleaning); a POS sends
// session events instead (seated, ordered, paid, left). Both are kept as they came, late ones
// included, and turned into dining sessions per table (start, end, people, source), which the
// figures a restaurant runs by come from: 上座率, 座位利用率, 翻台率, 平均用餐时长. A table nothing
// has reported for three hours is the twin's again, and its simulated sessions are kept too.
package tables

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"smartfactory/internal/engine"
	"smartfactory/internal/mapping"
	"smartfactory/internal/tags"
)

// Table states a report may give.
const (
	Free     = "free"
	Occupied = "occupied"
	Reserved = "reserved"
	Cleaning = "cleaning"
)

// Session events a POS may send.
const (
	Seated  = "seated"
	Ordered = "ordered"
	Paid    = "paid"
	Left    = "left"
)

// Sim is the source of the twin's own sessions.
const Sim = "sim"

// Governs is how long a report keeps a table out of the simulation.
const Governs = 3 * time.Hour

// Field is the convention field a table publishes its state on (sf/<company>/<plant>/<table>/table),
// FieldSession a POS its session events.
const (
	Field        = "table"
	FieldSession = "session"
)

var states = []string{Free, Occupied, Reserved, Cleaning}
var events = []string{Seated, Ordered, Paid, Left}

// StateLabel and EventLabel are how people read them.
var StateLabel = map[string]string{Free: "空闲", Occupied: "就餐中", Reserved: "已预订", Cleaning: "清台中"}
var EventLabel = map[string]string{Seated: "入座", Ordered: "点单", Paid: "结账", Left: "离开"}

var shanghai = func() *time.Location {
	if l, err := time.LoadLocation("Asia/Shanghai"); err == nil {
		return l
	}
	return time.FixedZone("CST", 8*3600)
}()

// DayStart is the start of the business day t falls in: 05:00 local, so a late supper counts
// with the evening before it.
func DayStart(t time.Time) time.Time {
	l := t.In(shanghai)
	d := time.Date(l.Year(), l.Month(), l.Day(), 5, 0, 0, 0, shanghai)
	if l.Before(d) {
		d = d.AddDate(0, 0, -1)
	}
	return d
}

// Twin is what the service needs of the engine.
type Twin interface {
	CompanyIDs() []string
	TableList(company string) []engine.Table
	ReportTable(company, plant, table, state string, people int, since time.Time, source string) bool
	TableSessions(company string) []engine.SimSession
}

// Service keeps the reports, events and sessions of every company in {data}/tables.db.
type Service struct {
	Twin Twin
	Hub  *tags.Hub
	Log  *slog.Logger
	Now  func() time.Time

	db *sql.DB
	// mu serialises writes: rebuilding a table's sessions reads then writes
	mu sync.Mutex
}

// Open opens (or creates) the store in dir.
func Open(dir string, twin Twin, hub *tags.Hub, log *slog.Logger) (*Service, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "tables.db")+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS reports (company TEXT NOT NULL, plant TEXT NOT NULL, tbl TEXT NOT NULL,
			state TEXT NOT NULL, people INTEGER NOT NULL, since INTEGER NOT NULL, source TEXT NOT NULL, received INTEGER NOT NULL,
			PRIMARY KEY (company, plant, tbl, since, state, source)) WITHOUT ROWID;
		CREATE TABLE IF NOT EXISTS events (company TEXT NOT NULL, plant TEXT NOT NULL, tbl TEXT NOT NULL,
			event TEXT NOT NULL, people INTEGER NOT NULL, at INTEGER NOT NULL, source TEXT NOT NULL, received INTEGER NOT NULL,
			PRIMARY KEY (company, plant, tbl, at, event, source)) WITHOUT ROWID;
		CREATE TABLE IF NOT EXISTS sessions (company TEXT NOT NULL, plant TEXT NOT NULL, tbl TEXT NOT NULL,
			start INTEGER NOT NULL, end INTEGER, people INTEGER NOT NULL, source TEXT NOT NULL,
			PRIMARY KEY (company, plant, tbl, start)) WITHOUT ROWID;
		CREATE INDEX IF NOT EXISTS sessions_start ON sessions (company, start);`); err != nil {
		db.Close()
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{Twin: twin, Hub: hub, Log: log, Now: time.Now, db: db}, nil
}

func (s *Service) Close() error { return s.db.Close() }

// Report is one table's state from a moment on, as a reporter sends it.
type Report struct {
	Table string `json:"table"`
	// Plant may be left out when the company has tables in one plant only.
	Plant  string `json:"plant,omitempty"`
	State  string `json:"state"`
	People int    `json:"people,omitempty"`
	// Since is when the table got into that state: ms or RFC 3339; now when left out.
	Since  any    `json:"since,omitempty"`
	Source string `json:"source,omitempty"`
}

// Event is a POS's session event.
type Event struct {
	Table  string `json:"table"`
	Plant  string `json:"plant,omitempty"`
	Event  string `json:"event"`
	People int    `json:"people,omitempty"`
	At     any    `json:"at,omitempty"`
	Source string `json:"source,omitempty"`
}

// Outcome is what became of one report or event.
type Outcome struct {
	Table string `json:"table"`
	Plant string `json:"plant,omitempty"`
	// Status: accepted (new, and the twin shows it), late (new but older than what the table
	// last reported: kept, the twin keeps the newer), duplicate (sent before), unknown (no such
	// table), invalid (Error says why).
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// Result counts the outcomes of a batch, with each one in the order sent.
type Result struct {
	Accepted  int       `json:"accepted"`
	Late      int       `json:"late"`
	Duplicate int       `json:"duplicate"`
	Unknown   int       `json:"unknown"`
	Invalid   int       `json:"invalid"`
	Items     []Outcome `json:"items"`
}

func (r *Result) add(o Outcome) {
	switch o.Status {
	case "accepted":
		r.Accepted++
	case "late":
		r.Late++
	case "duplicate":
		r.Duplicate++
	case "unknown":
		r.Unknown++
	default:
		r.Invalid++
	}
	r.Items = append(r.Items, o)
}

// ErrUnknownCompany is a company the engine does not run; ErrNoTable and ErrAmbiguous a table
// the company does not have, or one id in several plants.
var (
	ErrUnknownCompany = errors.New("unknown company")
	ErrNoTable        = errors.New("no such table")
	ErrAmbiguous      = errors.New("several plants have this table: name the plant")
)

// resolve finds the plant of a table: the one named, or the only plant that has a table of that id.
func resolve(list []engine.Table, plant, table string) (engine.Table, string) {
	var hits []engine.Table
	for _, t := range list {
		if t.ID == table && (plant == "" || t.Plant == plant) {
			hits = append(hits, t)
		}
	}
	switch {
	case len(hits) == 1:
		return hits[0], ""
	case len(hits) > 1:
		plants := []string{}
		for _, h := range hits {
			plants = append(plants, h.Plant)
		}
		return engine.Table{}, fmt.Sprintf("几个门店都有 %s（%s），写上 plant", table, strings.Join(plants, "、"))
	}
	return engine.Table{}, ""
}

// when reads a report's time: ms, s or RFC 3339; now when absent; now (and the reason) when it
// lies more than five minutes ahead of the server's clock.
func when(v any, now time.Time) (time.Time, error) {
	if v == nil || v == "" {
		return now, nil
	}
	switch n := v.(type) {
	case int64:
		v = float64(n)
	case int:
		v = float64(n)
	case time.Time:
		v = n.Format(time.RFC3339Nano)
	}
	t, ok := mapping.ParseTime(v)
	if !ok {
		return time.Time{}, fmt.Errorf("时间 %v 看不懂：用毫秒时间戳或 RFC 3339", v)
	}
	if t.After(now.Add(mapping.Future)) {
		return now, nil
	}
	return t, nil
}

// Report keeps a batch of table states and shows the newest of each table on the twin.
func (s *Service) Report(company string, in []Report, by string) (Result, error) {
	if !slices.Contains(s.Twin.CompanyIDs(), company) {
		return Result{}, ErrUnknownCompany
	}
	list := s.Twin.TableList(company)
	now := s.Now()
	res := Result{Items: []Outcome{}}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range in {
		o := Outcome{Table: r.Table, Plant: r.Plant}
		t, why := resolve(list, r.Plant, r.Table)
		if why != "" {
			o.Status, o.Error = "invalid", why
			res.add(o)
			continue
		}
		if t.ID == "" {
			o.Status = "unknown"
			res.add(o)
			continue
		}
		o.Plant = t.Plant
		if !slices.Contains(states, r.State) {
			o.Status, o.Error = "invalid", fmt.Sprintf("state 要是 %s", strings.Join(states, "、"))
			res.add(o)
			continue
		}
		since, err := when(r.Since, now)
		if err != nil {
			o.Status, o.Error = "invalid", err.Error()
			res.add(o)
			continue
		}
		people := max(0, r.People)
		if r.State == Occupied && people == 0 {
			people = min(2, max(1, t.Seats))
		}
		if r.State != Occupied {
			people = 0
		}
		src := source(r.Source, by)
		status, err := s.keep(company, t.Plant, t.ID, r.State, people, since, src, now)
		if err != nil {
			return res, err
		}
		o.Status = status
		res.add(o)
	}
	return res, nil
}

// source is who a report says it is from, else the token holder.
func source(src, by string) string {
	src = strings.TrimSpace(src)
	if src == "" {
		src = strings.TrimSpace(by)
	}
	if src == "" || src == Sim {
		src = "reporter"
	}
	return src
}

// keep stores one state and, when it is the table's newest, hands it to the twin.
func (s *Service) keep(company, plant, table, state string, people int, since time.Time, src string, now time.Time) (string, error) {
	latest, err := s.latest(company, plant, table)
	if err != nil {
		return "", err
	}
	r, err := s.db.Exec(`INSERT INTO reports VALUES (?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`,
		company, plant, table, state, people, since.UnixMilli(), src, now.UnixMilli())
	if err != nil {
		return "", err
	}
	if n, _ := r.RowsAffected(); n == 0 {
		return "duplicate", nil
	}
	if err := s.rebuild(company, plant, table, since); err != nil {
		return "", err
	}
	if !latest.IsZero() && since.Before(latest) {
		return "late", nil
	}
	s.Twin.ReportTable(company, plant, table, state, people, since, src)
	return "accepted", nil
}

// Events keeps a batch of a POS's session events; seated, paid and left move the table on the twin.
func (s *Service) Events(company string, in []Event, by string) (Result, error) {
	if !slices.Contains(s.Twin.CompanyIDs(), company) {
		return Result{}, ErrUnknownCompany
	}
	list := s.Twin.TableList(company)
	now := s.Now()
	res := Result{Items: []Outcome{}}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, e := range in {
		o := Outcome{Table: e.Table, Plant: e.Plant}
		t, why := resolve(list, e.Plant, e.Table)
		if why != "" {
			o.Status, o.Error = "invalid", why
			res.add(o)
			continue
		}
		if t.ID == "" {
			o.Status = "unknown"
			res.add(o)
			continue
		}
		o.Plant = t.Plant
		if !slices.Contains(events, e.Event) {
			o.Status, o.Error = "invalid", fmt.Sprintf("event 要是 %s", strings.Join(events, "、"))
			res.add(o)
			continue
		}
		at, err := when(e.At, now)
		if err != nil {
			o.Status, o.Error = "invalid", err.Error()
			res.add(o)
			continue
		}
		src := source(e.Source, by)
		latest, err := s.latest(company, t.Plant, t.ID)
		if err != nil {
			return res, err
		}
		r, err := s.db.Exec(`INSERT INTO events VALUES (?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`,
			company, t.Plant, t.ID, e.Event, max(0, e.People), at.UnixMilli(), src, now.UnixMilli())
		if err != nil {
			return res, err
		}
		if n, _ := r.RowsAffected(); n == 0 {
			o.Status = "duplicate"
			res.add(o)
			continue
		}
		if err := s.rebuild(company, t.Plant, t.ID, at); err != nil {
			return res, err
		}
		switch {
		case !latest.IsZero() && at.Before(latest):
			o.Status = "late"
		default:
			o.Status = "accepted"
			switch e.Event {
			case Seated:
				s.Twin.ReportTable(company, t.Plant, t.ID, Occupied, max(1, e.People), at, src)
			case Paid, Left:
				// paid: the table is cleared once they go; left: it needs clearing
				s.Twin.ReportTable(company, t.Plant, t.ID, Cleaning, 0, at, src)
			case Ordered:
				// the table is still taken; a POS that never said seated opens the session here
				if open, _ := s.openSession(company, t.Plant, t.ID); open == nil {
					s.Twin.ReportTable(company, t.Plant, t.ID, Occupied, max(1, e.People), at, src)
				}
			}
		}
		res.add(o)
	}
	return res, nil
}

// latest is when the table's newest report or event happened (zero for none).
func (s *Service) latest(company, plant, table string) (time.Time, error) {
	var ms sql.NullInt64
	err := s.db.QueryRow(`SELECT max(t) FROM (SELECT max(since) AS t FROM reports WHERE company=? AND plant=? AND tbl=?
		UNION ALL SELECT max(at) FROM events WHERE company=? AND plant=? AND tbl=?)`, company, plant, table, company, plant, table).Scan(&ms)
	if err != nil || !ms.Valid {
		return time.Time{}, err
	}
	return time.UnixMilli(ms.Int64), nil
}

// lastReport is the source and time of the table's newest report or event.
func (s *Service) lastReport(company, plant, table string) (string, time.Time, error) {
	var src string
	var ms int64
	err := s.db.QueryRow(`SELECT source, t FROM (SELECT source, since AS t FROM reports WHERE company=? AND plant=? AND tbl=?
		UNION ALL SELECT source, at FROM events WHERE company=? AND plant=? AND tbl=?) ORDER BY t DESC LIMIT 1`,
		company, plant, table, company, plant, table).Scan(&src, &ms)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, nil
	}
	return src, time.UnixMilli(ms), err
}

// Run keeps the twin's own sessions of the tables nothing reports, and takes the tables'
// reports and session events that come in over MQTT (the convention topic), until ctx ends.
func (s *Service) Run(ctx context.Context) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	var sub *tags.Sub
	var in <-chan tags.Event
	if s.Hub != nil {
		sub = s.Hub.Subscribe()
		defer s.Hub.Unsubscribe(sub)
		in = sub.C
	}
	for {
		select {
		case <-ctx.Done():
			s.drain()
			return
		case <-tick.C:
			s.drain()
		case e := <-in:
			if e.Type == "tag" && e.Tag != nil {
				s.fromTag(*e.Tag)
			}
		}
	}
}

// drain stores the simulated sessions the engine opened and closed since the last call.
func (s *Service) drain() {
	for _, c := range s.Twin.CompanyIDs() {
		if err := s.KeepSim(c, s.Twin.TableSessions(c)); err != nil {
			s.Log.Warn("tables: simulated sessions", "company", c, "err", err)
		}
	}
}

// KeepSim stores simulated sessions, except for tables something reported within Governs of them.
func (s *Service) KeepSim(company string, list []engine.SimSession) error {
	if len(list) == 0 {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, x := range list {
		at := time.UnixMilli(x.Start)
		if x.End != nil {
			at = time.UnixMilli(*x.End)
		}
		latest, err := s.latest(company, x.Plant, x.Table)
		if err != nil {
			return err
		}
		if !latest.IsZero() && at.Sub(latest) < Governs && latest.Sub(at) < Governs {
			continue
		}
		var end any
		if x.End != nil {
			end = *x.End
		}
		if _, err := s.db.Exec(`INSERT INTO sessions VALUES (?,?,?,?,?,?,?) ON CONFLICT DO UPDATE SET end=excluded.end, people=excluded.people
			WHERE sessions.source=?`, company, x.Plant, x.Table, x.Start, end, x.People, Sim, Sim); err != nil {
			return err
		}
	}
	return nil
}

// fromTag takes a table's report or a POS event published on the convention topic:
// sf/<company>/<plant>/<table>/table {"state", "people", "since", "source"} and
// sf/<company>/<plant>/<table>/session {"event", "people", "at", "source"}.
func (s *Service) fromTag(t tags.Tag) {
	if t.Field != Field && t.Field != FieldSession {
		return
	}
	m, ok := t.Value.(map[string]any)
	if !ok {
		// a bare state word: "occupied"
		if w, isStr := t.Value.(string); isStr {
			m = map[string]any{"state": w}
		} else {
			return
		}
	}
	str := func(k string) string { v, _ := m[k].(string); return v }
	num := func(k string) int { v, _ := m[k].(float64); return int(v) }
	src := str("source")
	if src == "" {
		src = t.Source
	}
	var err error
	if t.Field == Field {
		_, err = s.Report(t.Company, []Report{{Table: t.Asset, Plant: t.Plant, State: str("state"), People: num("people"), Since: m["since"], Source: src}}, t.Source)
	} else {
		_, err = s.Events(t.Company, []Event{{Table: t.Asset, Plant: t.Plant, Event: str("event"), People: num("people"), At: m["at"], Source: src}}, t.Source)
	}
	if err != nil && !errors.Is(err, ErrUnknownCompany) {
		s.Log.Warn("tables: mqtt report", "company", t.Company, "table", t.Asset, "err", err)
	}
}

// IsReport reports whether a convention tag is a table report or session event (not a reading).
func IsReport(field string) bool { return field == Field || field == FieldSession }
