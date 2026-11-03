package plan

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Plan statuses: a company has at most one draft and one published plan; publishing a draft
// retires the plan it replaces.
const (
	Draft     = "draft"
	Published = "published"
	Retired   = "retired"
)

// Plan is a plan as kept: what it laid out, who made it, when.
type Plan struct {
	ID        string     `json:"id"`
	Status    string     `json:"status"`
	Goal      Goal       `json:"goal"`
	Created   time.Time  `json:"created"`
	By        string     `json:"by"`
	Published *time.Time `json:"published,omitempty"`
	// Note says how it came about (重新排, 挪了 X …).
	Note   string  `json:"note,omitempty"`
	Result *Result `json:"result"`
}

// Settings are how a company plans: the goal, what to do with orders short of material, when
// their material is expected, stops planned for lines, and the orders locked where they are.
type Settings struct {
	Goal Goal `json:"goal"`
	// Material: hold (an order short of material with no arrival known is not planned) or plan
	// (it is planned at its shortage, flagged).
	Material string `json:"material"`
	// Arrive is when the material of an order short of it is expected (order no → time).
	Arrive map[string]time.Time `json:"arrive"`
	Stops  []Stop               `json:"stops"`
	Locks  map[string]Lock      `json:"locks"`
}

func (s *Settings) normalize() {
	if GoalLabel[s.Goal] == "" {
		s.Goal = Due
	}
	if s.Material != AsIs {
		s.Material = Hold
	}
	if s.Arrive == nil {
		s.Arrive = map[string]time.Time{}
	}
	if s.Stops == nil {
		s.Stops = []Stop{}
	}
	if s.Locks == nil {
		s.Locks = map[string]Lock{}
	}
}

// Store keeps every company's plans and planning settings in {data}/plans.db.
type Store struct{ db *sql.DB }

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "plans.db")+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS plans (
		company TEXT NOT NULL, id TEXT NOT NULL, status TEXT NOT NULL, goal TEXT NOT NULL, created INTEGER NOT NULL, by TEXT NOT NULL,
		published INTEGER NOT NULL, note TEXT NOT NULL, body TEXT NOT NULL, PRIMARY KEY (company, id));
		CREATE INDEX IF NOT EXISTS plans_status ON plans(company, status);
		CREATE TABLE IF NOT EXISTS plan_settings (company TEXT PRIMARY KEY, body TEXT NOT NULL)`); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) get(company, status string) (*Plan, error) {
	var p Plan
	var created, published int64
	var body string
	err := s.db.QueryRow(`SELECT id, status, goal, created, by, published, note, body FROM plans WHERE company=? AND status=? ORDER BY created DESC LIMIT 1`, company, status).
		Scan(&p.ID, &p.Status, &p.Goal, &created, &p.By, &published, &p.Note, &body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	p.Created = time.UnixMilli(created)
	if published > 0 {
		t := time.UnixMilli(published)
		p.Published = &t
	}
	if err := json.Unmarshal([]byte(body), &p.Result); err != nil {
		return nil, err
	}
	return &p, nil
}

// saveDraft replaces the company's draft.
func (s *Store) saveDraft(company string, p *Plan) error {
	body, err := json.Marshal(p.Result)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM plans WHERE company=? AND status=?`, company, Draft); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO plans VALUES (?,?,?,?,?,?,0,?,?)`, company, p.ID, Draft, p.Goal, p.Created.UnixMilli(), p.By, p.Note, string(body)); err != nil {
		return err
	}
	return tx.Commit()
}

// publish makes the draft the plan in force; the one it replaces is retired (the last 20 kept).
func (s *Store) publish(company, id string, at time.Time) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE plans SET status=? WHERE company=? AND status=?`, Retired, company, Published); err != nil {
		return err
	}
	res, err := tx.Exec(`UPDATE plans SET status=?, published=? WHERE company=? AND id=? AND status=?`, Published, at.UnixMilli(), company, id, Draft)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("这份草稿已经不在了，重新排一次")
	}
	if _, err := tx.Exec(`DELETE FROM plans WHERE company=? AND status=? AND id NOT IN (SELECT id FROM plans WHERE company=? AND status=? ORDER BY created DESC LIMIT 20)`,
		company, Retired, company, Retired); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) discard(company string) error {
	_, err := s.db.Exec(`DELETE FROM plans WHERE company=? AND status=?`, company, Draft)
	return err
}

func (s *Store) settings(company string) (Settings, error) {
	var st Settings
	var body string
	err := s.db.QueryRow(`SELECT body FROM plan_settings WHERE company=?`, company).Scan(&body)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return st, err
	}
	if body != "" {
		json.Unmarshal([]byte(body), &st)
	}
	st.normalize()
	return st, nil
}

func (s *Store) saveSettings(company string, st Settings) error {
	b, err := json.Marshal(st)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO plan_settings VALUES (?,?) ON CONFLICT(company) DO UPDATE SET body=excluded.body`, company, string(b))
	return err
}
