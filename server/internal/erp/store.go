// Package erp connects a company's ERP (or MES, or whatever plans its production) to the twin:
// production orders come in — pushed to the order API or pulled from the ERP's own — each is
// routed to a line that makes its part, the lines run them in due-date order, and what started
// and finished goes back to the ERP as a signed callback and through the same API.
package erp

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"smartfactory/internal/engine"
)

// Order statuses.
const (
	Queued    = "queued"
	Running   = "running"
	Done      = "done"
	Cancelled = "cancelled"
)

// Order is a production order as the twin keeps it.
type Order struct {
	No       string     `json:"no"`
	PartNo   string     `json:"partNo,omitempty"`
	Product  string     `json:"product,omitempty"`
	Name     string     `json:"name,omitempty"`
	Qty      int        `json:"qty"`
	Due      *time.Time `json:"due,omitempty"`
	Customer string     `json:"customer,omitempty"`
	Price    float64    `json:"price,omitempty"`
	// Plant and Line are where it runs (ids), with their names.
	Plant     string `json:"plant"`
	PlantName string `json:"plantName"`
	Line      string `json:"line"`
	LineName  string `json:"lineName"`
	// Problem is why it cannot run (no line makes its part any more).
	Problem  string     `json:"problem,omitempty"`
	Status   string     `json:"status"`
	Good     int        `json:"good"`
	Scrap    int        `json:"scrap"`
	Source   string     `json:"source"` // push | pull | console
	Created  time.Time  `json:"created"`
	Started  *time.Time `json:"started,omitempty"`
	Finished *time.Time `json:"finished,omitempty"`
	Updated  time.Time  `json:"updated"`
}

// Input is an order as the ERP sends it. Plant and Line, when given, say where it must run.
type Input struct {
	No       string     `json:"no"`
	PartNo   string     `json:"partNo,omitempty"`
	Product  string     `json:"product,omitempty"`
	Name     string     `json:"name,omitempty"`
	Qty      int        `json:"qty"`
	Due      *time.Time `json:"due,omitempty"`
	Customer string     `json:"customer,omitempty"`
	Price    float64    `json:"price,omitempty"`
	Plant    string     `json:"plant,omitempty"`
	Line     string     `json:"line,omitempty"`
}

// Store keeps every company's orders and connection settings in {data}/erp.db.
type Store struct{ db *sql.DB }

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "erp.db")+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS orders (
		company TEXT NOT NULL, no TEXT NOT NULL, part_no TEXT NOT NULL, product TEXT NOT NULL, name TEXT NOT NULL,
		qty INTEGER NOT NULL, due INTEGER NOT NULL, customer TEXT NOT NULL, price REAL NOT NULL,
		want_plant TEXT NOT NULL, want_line TEXT NOT NULL, plant TEXT NOT NULL, plant_name TEXT NOT NULL, line TEXT NOT NULL, line_name TEXT NOT NULL,
		problem TEXT NOT NULL, status TEXT NOT NULL, good INTEGER NOT NULL, scrap INTEGER NOT NULL, source TEXT NOT NULL,
		created INTEGER NOT NULL, started INTEGER NOT NULL, finished INTEGER NOT NULL, updated INTEGER NOT NULL,
		PRIMARY KEY (company, no));
		CREATE INDEX IF NOT EXISTS orders_status ON orders(company, status);
		CREATE TABLE IF NOT EXISTS settings (company TEXT PRIMARY KEY, config TEXT NOT NULL, status TEXT NOT NULL)`); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

const orderCols = `no, part_no, product, name, qty, due, customer, price, plant, plant_name, line, line_name, problem, status, good, scrap, source, created, started, finished, updated`

func ms(t time.Time) int64 { return t.UnixMilli() }

func when(v int64) *time.Time {
	if v == 0 {
		return nil
	}
	t := time.UnixMilli(v)
	return &t
}

func scanOrder(sc interface{ Scan(...any) error }) (Order, error) {
	var o Order
	var due, created, started, finished, updated int64
	err := sc.Scan(&o.No, &o.PartNo, &o.Product, &o.Name, &o.Qty, &due, &o.Customer, &o.Price, &o.Plant, &o.PlantName, &o.Line, &o.LineName,
		&o.Problem, &o.Status, &o.Good, &o.Scrap, &o.Source, &created, &started, &finished, &updated)
	o.Due, o.Started, o.Finished = when(due), when(started), when(finished)
	o.Created, o.Updated = time.UnixMilli(created), time.UnixMilli(updated)
	return o, err
}

// Get is one order, or nil.
func (s *Store) Get(company, no string) (*Order, error) {
	o, err := scanOrder(s.db.QueryRow(`SELECT `+orderCols+` FROM orders WHERE company=? AND no=?`, company, no))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &o, nil
}

// Filter narrows a listing: statuses, and text found in the number, part, product, customer or line.
type Filter struct {
	Status []string
	Q      string
	Limit  int
}

// List is orders running first, then queued in the order they will run, then the rest newest first.
func (s *Store) List(company string, f Filter) ([]Order, error) {
	q := `SELECT ` + orderCols + ` FROM orders WHERE company=?`
	args := []any{company}
	if len(f.Status) > 0 {
		q += ` AND status IN (?` + strings.Repeat(",?", len(f.Status)-1) + `)`
		for _, st := range f.Status {
			args = append(args, st)
		}
	}
	if t := strings.TrimSpace(f.Q); t != "" {
		q += ` AND (no LIKE ? OR part_no LIKE ? OR product LIKE ? OR name LIKE ? OR customer LIKE ? OR line_name LIKE ?)`
		like := "%" + t + "%"
		args = append(args, like, like, like, like, like, like)
	}
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 200
	}
	q += ` ORDER BY CASE status WHEN 'running' THEN 0 WHEN 'queued' THEN 1 ELSE 2 END,
		CASE WHEN status='queued' THEN (CASE due WHEN 0 THEN 9e15 ELSE due END) ELSE -max(finished, updated) END, created LIMIT ?`
	args = append(args, f.Limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Order{}
	for rows.Next() {
		o, err := scanOrder(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// Counts is how many orders there are in each status.
func (s *Store) Counts(company string) (map[string]int, error) {
	rows, err := s.db.Query(`SELECT status, count(*) FROM orders WHERE company=? GROUP BY status`, company)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{Queued: 0, Running: 0, Done: 0, Cancelled: 0}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out[st] = n
	}
	return out, rows.Err()
}

// Outcome is what storing an order did.
type Outcome string

const (
	Created   Outcome = "created"
	Updated   Outcome = "updated"
	Unchanged Outcome = "unchanged"
	Requeued  Outcome = "requeued"
)

var errFinished = errors.New("已经完成，不再修改")

// put stores an order the ERP sent, routed to r. A queued order takes every change; a running
// one its quantity, due date, customer and price; a finished one nothing; a withdrawn one is
// queued again.
func (s *Store) put(company string, in Input, r engine.Route, source string, now time.Time) (Outcome, error) {
	old, err := s.Get(company, in.No)
	if err != nil {
		return "", err
	}
	due := int64(0)
	if in.Due != nil {
		due = ms(*in.Due)
	}
	switch {
	case old == nil:
		_, err = s.db.Exec(`INSERT INTO orders VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			company, in.No, in.PartNo, in.Product, in.Name, in.Qty, due, in.Customer, in.Price, in.Plant, in.Line,
			r.Plant, r.PlantName, r.Line, r.LineName, "", Queued, 0, 0, source, ms(now), 0, 0, ms(now))
		return Created, err
	case old.Status == Done:
		return Unchanged, errFinished
	case old.Status == Running:
		if old.Qty == in.Qty && ms0(old.Due) == due && old.Customer == in.Customer && old.Price == in.Price {
			return Unchanged, nil
		}
		_, err = s.db.Exec(`UPDATE orders SET qty=?, due=?, customer=?, price=?, updated=? WHERE company=? AND no=?`,
			in.Qty, due, in.Customer, in.Price, ms(now), company, in.No)
		return Updated, err
	}
	same := old.Status == Queued && old.PartNo == in.PartNo && old.Product == in.Product && old.Name == in.Name && old.Qty == in.Qty &&
		ms0(old.Due) == due && old.Customer == in.Customer && old.Price == in.Price && old.Plant == r.Plant && old.Line == r.Line && old.Problem == ""
	if same {
		return Unchanged, nil
	}
	_, err = s.db.Exec(`UPDATE orders SET part_no=?, product=?, name=?, qty=?, due=?, customer=?, price=?, want_plant=?, want_line=?,
		plant=?, plant_name=?, line=?, line_name=?, problem='', status=?, good=0, scrap=0, started=0, finished=0, updated=? WHERE company=? AND no=?`,
		in.PartNo, in.Product, in.Name, in.Qty, due, in.Customer, in.Price, in.Plant, in.Line, r.Plant, r.PlantName, r.Line, r.LineName,
		Queued, ms(now), company, in.No)
	if old.Status == Cancelled {
		return Requeued, err
	}
	return Updated, err
}

func ms0(t *time.Time) int64 {
	if t == nil {
		return 0
	}
	return ms(*t)
}

// cancel withdraws an order that has not finished.
func (s *Store) cancel(company, no string, now time.Time) (*Order, error) {
	o, err := s.Get(company, no)
	if err != nil || o == nil {
		return nil, err
	}
	if o.Status == Done {
		return o, errFinished
	}
	if o.Status != Cancelled {
		if _, err := s.db.Exec(`UPDATE orders SET status=?, updated=? WHERE company=? AND no=?`, Cancelled, ms(now), company, no); err != nil {
			return nil, err
		}
		o.Status = Cancelled
	}
	return o, nil
}

// queue is what the lines are to run: queued orders by line, earliest due first (orders without
// a due date after those with one, in the order they came).
func (s *Store) queue(company, mode string) (*engine.OrderQueue, error) {
	rows, err := s.db.Query(`SELECT no, part_no, name, qty, due, customer, price, plant, line, status FROM orders
		WHERE company=? AND status IN ('queued','running') ORDER BY CASE due WHEN 0 THEN 9e15 ELSE due END, created`, company)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	q := &engine.OrderQueue{Mode: mode, Queue: map[string][]engine.ExtOrder{}, Active: []string{}}
	for rows.Next() {
		var o engine.ExtOrder
		var plant, line, status string
		if err := rows.Scan(&o.No, &o.PartNo, &o.Name, &o.Qty, &o.Due, &o.Customer, &o.Price, &plant, &line, &status); err != nil {
			return nil, err
		}
		q.Active = append(q.Active, o.No)
		if status == Queued && line != "" {
			k := plant + "/" + line
			q.Queue[k] = append(q.Queue[k], o)
		}
	}
	return q, rows.Err()
}

func (s *Store) started(company string, e engine.OrderEvent, now time.Time) (bool, error) {
	res, err := s.db.Exec(`UPDATE orders SET status=?, plant=?, plant_name=?, line=?, line_name=?, problem='', started=?, updated=? WHERE company=? AND no=? AND status=?`,
		Running, e.Plant, e.PlantName, e.Line, e.LineName, ms(now), ms(now), company, e.No, Queued)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *Store) finished(company string, e engine.OrderEvent, now time.Time) (bool, error) {
	res, err := s.db.Exec(`UPDATE orders SET status=?, good=?, scrap=?, finished=?, updated=? WHERE company=? AND no=? AND status IN ('queued','running')`,
		Done, e.Good, e.Scrap, ms(now), ms(now), company, e.No)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *Store) progress(company string, e engine.OrderEvent) error {
	_, err := s.db.Exec(`UPDATE orders SET good=?, scrap=? WHERE company=? AND no=? AND status=? AND (good<>? OR scrap<>?)`,
		e.Good, e.Scrap, company, e.No, Running, e.Good, e.Scrap)
	return err
}

// requeueLost puts back in the queue the orders marked running that no line is running (the
// twin's state was lost); it says how many.
func (s *Store) requeueLost(company string, running map[string]bool, now time.Time) (int, error) {
	rows, err := s.db.Query(`SELECT no FROM orders WHERE company=? AND status=?`, company, Running)
	if err != nil {
		return 0, err
	}
	var lost []string
	for rows.Next() {
		var no string
		rows.Scan(&no)
		if !running[no] {
			lost = append(lost, no)
		}
	}
	rows.Close()
	for _, no := range lost {
		if _, err := s.db.Exec(`UPDATE orders SET status=?, good=0, scrap=0, started=0, updated=? WHERE company=? AND no=?`, Queued, ms(now), company, no); err != nil {
			return 0, err
		}
	}
	return len(lost), nil
}

// ahead is the units queued for each line ("plant/line"), not counting the order no.
func (s *Store) ahead(company, no string) (map[string]int, error) {
	rows, err := s.db.Query(`SELECT plant, line, sum(qty) FROM orders WHERE company=? AND status=? AND line<>'' AND no<>? GROUP BY plant, line`, company, Queued, no)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var plant, line string
		var n int
		if err := rows.Scan(&plant, &line, &n); err != nil {
			return nil, err
		}
		out[plant+"/"+line] = n
	}
	return out, rows.Err()
}

// queuedRoutes is every queued order with where it asked to run, for routing again.
func (s *Store) queuedRoutes(company string) ([]Input, error) {
	rows, err := s.db.Query(`SELECT no, part_no, product, want_plant, want_line FROM orders WHERE company=? AND status=?`, company, Queued)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Input
	for rows.Next() {
		var in Input
		if err := rows.Scan(&in.No, &in.PartNo, &in.Product, &in.Plant, &in.Line); err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

func (s *Store) reroute(company, no string, r engine.Route, problem string, now time.Time) error {
	_, err := s.db.Exec(`UPDATE orders SET plant=?, plant_name=?, line=?, line_name=?, problem=?, updated=? WHERE company=? AND no=? AND status=?`,
		r.Plant, r.PlantName, r.Line, r.LineName, problem, ms(now), company, no, Queued)
	return err
}

// settings are a company's connection config and its last status.
func (s *Store) settings(company string) (Config, Status, error) {
	var c, st string
	err := s.db.QueryRow(`SELECT config, status FROM settings WHERE company=?`, company).Scan(&c, &st)
	if errors.Is(err, sql.ErrNoRows) {
		return Config{}, Status{}, nil
	}
	if err != nil {
		return Config{}, Status{}, err
	}
	var cfg Config
	var status Status
	json.Unmarshal([]byte(c), &cfg)
	json.Unmarshal([]byte(st), &status)
	return cfg, status, nil
}

func (s *Store) saveConfig(company string, cfg Config) error {
	b, _ := json.Marshal(cfg)
	_, err := s.db.Exec(`INSERT INTO settings VALUES (?,?,'{}') ON CONFLICT(company) DO UPDATE SET config=excluded.config`, company, string(b))
	return err
}

func (s *Store) saveStatus(company string, st Status) error {
	b, _ := json.Marshal(st)
	_, err := s.db.Exec(`INSERT INTO settings VALUES (?,'{}',?) ON CONFLICT(company) DO UPDATE SET status=excluded.status`, company, string(b))
	return err
}

// companies with settings.
func (s *Store) companies() ([]string, error) {
	rows, err := s.db.Query(`SELECT company FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		rows.Scan(&c)
		out = append(out, c)
	}
	return out, rows.Err()
}
