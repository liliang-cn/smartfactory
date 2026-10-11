package calib

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"smartfactory/internal/engine"
)

// Store keeps every company's residuals (per machine, field and minute), the last fit of each
// machine and the calibration applied to it, in {data}/calib.db.
type Store struct{ db *sql.DB }

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "calib.db")+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS residuals (
		company TEXT NOT NULL, plant TEXT NOT NULL, asset TEXT NOT NULL, field TEXT NOT NULL, minute INTEGER NOT NULL,
		seconds REAL NOT NULL, sum REAL NOT NULL, abs REAL NOT NULL, ok REAL NOT NULL,
		PRIMARY KEY (company, plant, asset, field, minute)) WITHOUT ROWID;
		CREATE INDEX IF NOT EXISTS residuals_minute ON residuals (minute);
		CREATE TABLE IF NOT EXISTS fits (company TEXT NOT NULL, plant TEXT NOT NULL, asset TEXT NOT NULL, fit TEXT NOT NULL, at INTEGER NOT NULL,
		PRIMARY KEY (company, plant, asset));
		CREATE TABLE IF NOT EXISTS applied (company TEXT NOT NULL, plant TEXT NOT NULL, asset TEXT NOT NULL, params TEXT NOT NULL, at INTEGER NOT NULL, by TEXT NOT NULL,
		PRIMARY KEY (company, plant, asset))`); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

// addResiduals adds what the models got wrong to the minute they fall in.
func (s *Store) addResiduals(company string, rs []engine.Residual, at time.Time) error {
	if len(rs) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	minute := at.Truncate(time.Minute).UnixMilli()
	for _, r := range rs {
		if _, err := tx.Exec(`INSERT INTO residuals VALUES (?,?,?,?,?,?,?,?,?)
			ON CONFLICT DO UPDATE SET seconds=seconds+excluded.seconds, sum=sum+excluded.sum, abs=abs+excluded.abs, ok=ok+excluded.ok`,
			company, r.Plant, r.Asset, r.Field, minute, r.Seconds, r.Mean*r.Seconds, r.MAE*r.Seconds, r.Within*r.Seconds); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ResidualPoint is a minute of a machine's residual in one field.
type ResidualPoint struct {
	T       int64   `json:"t"`
	Seconds float64 `json:"seconds"`
	Mean    float64 `json:"mean"`
	MAE     float64 `json:"mae"`
	Within  float64 `json:"within"`
}

// residuals of a machine's field over a range, by minute (or coarser buckets of step minutes).
func (s *Store) residuals(company, plant, asset, field string, from, to time.Time, step time.Duration) ([]ResidualPoint, error) {
	bucket := max(int64(60_000), step.Milliseconds())
	rows, err := s.db.Query(`SELECT (minute / ?) * ?, sum(seconds), sum(sum), sum(abs), sum(ok) FROM residuals
		WHERE company=? AND plant=? AND asset=? AND field=? AND minute >= ? AND minute < ? GROUP BY 1 ORDER BY 1`,
		bucket, bucket, company, plant, asset, field, from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ResidualPoint{}
	for rows.Next() {
		var p ResidualPoint
		var sum, abs, ok float64
		if err := rows.Scan(&p.T, &p.Seconds, &sum, &abs, &ok); err != nil {
			return nil, err
		}
		if p.Seconds > 0 {
			p.Mean, p.MAE, p.Within = r(sum/p.Seconds, 3), r(abs/p.Seconds, 3), r(ok/p.Seconds, 3)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Summary is a machine's residual in one field over a span.
type Summary struct {
	Field   string  `json:"field"`
	Seconds float64 `json:"seconds"`
	Mean    float64 `json:"mean"`
	MAE     float64 `json:"mae"`
	Within  float64 `json:"within"`
}

// summaries are every machine's residuals since from, by "plant/asset".
func (s *Store) summaries(company string, from time.Time) (map[string][]Summary, error) {
	rows, err := s.db.Query(`SELECT plant, asset, field, sum(seconds), sum(sum), sum(abs), sum(ok) FROM residuals
		WHERE company=? AND minute >= ? GROUP BY plant, asset, field ORDER BY plant, asset, field`, company, from.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]Summary{}
	for rows.Next() {
		var plant, asset string
		var x Summary
		var sum, abs, ok float64
		if err := rows.Scan(&plant, &asset, &x.Field, &x.Seconds, &sum, &abs, &ok); err != nil {
			return nil, err
		}
		if x.Seconds > 0 {
			x.Mean, x.MAE, x.Within = r(sum/x.Seconds, 3), r(abs/x.Seconds, 3), r(ok/x.Seconds, 3)
		}
		out[plant+"/"+asset] = append(out[plant+"/"+asset], x)
	}
	return out, rows.Err()
}

func (s *Store) prune(before time.Time) error {
	_, err := s.db.Exec(`DELETE FROM residuals WHERE minute < ?`, before.UnixMilli())
	return err
}

// Fitted is a machine's last fit.
type Fitted struct {
	At     time.Time `json:"at"`
	From   time.Time `json:"from"`
	To     time.Time `json:"to"`
	Params []Param   `json:"params"`
	Replay []Replay  `json:"replay"`
}

func (s *Store) saveFit(company, plant, asset string, f Fitted) error {
	b, _ := json.Marshal(f)
	_, err := s.db.Exec(`INSERT INTO fits VALUES (?,?,?,?,?) ON CONFLICT DO UPDATE SET fit=excluded.fit, at=excluded.at`,
		company, plant, asset, string(b), f.At.UnixMilli())
	return err
}

func (s *Store) fits(company string) (map[string]Fitted, error) {
	rows, err := s.db.Query(`SELECT plant, asset, fit FROM fits WHERE company=?`, company)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Fitted{}
	for rows.Next() {
		var plant, asset, b string
		if err := rows.Scan(&plant, &asset, &b); err != nil {
			return nil, err
		}
		var f Fitted
		if json.Unmarshal([]byte(b), &f) == nil {
			out[plant+"/"+asset] = f
		}
	}
	return out, rows.Err()
}

// Applied is a calibration in force on a machine.
type Applied struct {
	Params engine.Params `json:"params"`
	At     time.Time     `json:"at"`
	By     string        `json:"by"`
}

func (s *Store) apply(company, plant, asset string, a Applied) error {
	b, _ := json.Marshal(a.Params)
	_, err := s.db.Exec(`INSERT INTO applied VALUES (?,?,?,?,?,?) ON CONFLICT DO UPDATE SET params=excluded.params, at=excluded.at, by=excluded.by`,
		company, plant, asset, string(b), a.At.UnixMilli(), a.By)
	return err
}

func (s *Store) revert(company, plant, asset string) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM applied WHERE company=? AND plant=? AND asset=?`, company, plant, asset)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

func (s *Store) applied(company string) (map[string]Applied, error) {
	rows, err := s.db.Query(`SELECT plant, asset, params, at, by FROM applied WHERE company=?`, company)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Applied{}
	for rows.Next() {
		var plant, asset, b, by string
		var at int64
		if err := rows.Scan(&plant, &asset, &b, &at, &by); err != nil {
			return nil, err
		}
		var a Applied
		if err := json.Unmarshal([]byte(b), &a.Params); err != nil {
			return nil, errors.New("calibration of " + plant + "/" + asset + " unreadable")
		}
		a.At, a.By = time.UnixMilli(at), by
		out[plant+"/"+asset] = a
	}
	return out, rows.Err()
}

func (s *Store) companies() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT company FROM applied`)
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
