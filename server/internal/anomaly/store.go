package anomaly

import (
	"database/sql"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Store keeps the normals (a row per machine, field and state, written every few minutes, not
// per reading) and the hints, in {data}/anomaly.db.
type Store struct{ db *sql.DB }

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "anomaly.db")+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS baselines (company TEXT NOT NULL, plant TEXT NOT NULL, asset TEXT NOT NULL, field TEXT NOT NULL, state TEXT NOT NULL,
			n INTEGER NOT NULL, mean REAL NOT NULL, var REAL NOT NULL, short REAL NOT NULL, svar REAL NOT NULL, first INTEGER NOT NULL, last INTEGER NOT NULL,
			PRIMARY KEY (company, plant, asset, field, state)) WITHOUT ROWID;
		CREATE TABLE IF NOT EXISTS hints (id INTEGER PRIMARY KEY, company TEXT NOT NULL, plant TEXT NOT NULL, asset TEXT NOT NULL, field TEXT NOT NULL,
			state TEXT NOT NULL, kind TEXT NOT NULL, since INTEGER NOT NULL, ended INTEGER, value REAL NOT NULL, z REAL NOT NULL, peak REAL NOT NULL,
			mean REAL NOT NULL, sigma REAL NOT NULL, source TEXT NOT NULL, text TEXT NOT NULL);
		CREATE INDEX IF NOT EXISTS hints_company ON hints (company, since)`); err != nil {
		db.Close()
		return nil, err
	}
	// hints open when the server stopped: what became of them was not watched
	if _, err := db.Exec(`UPDATE hints SET ended = ? WHERE ended IS NULL`, time.Now().UnixMilli()); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

type row struct {
	company, plant, asset, field, state string
	stat                                Stat
}

func (s *Store) baselines() ([]row, error) {
	rows, err := s.db.Query(`SELECT company, plant, asset, field, state, n, mean, var, short, svar, first, last FROM baselines`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.company, &r.plant, &r.asset, &r.field, &r.state, &r.stat.N, &r.stat.Mean, &r.stat.Var, &r.stat.Short, &r.stat.ShortVar, &r.stat.First, &r.stat.Last); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) save(rs []row) error {
	if len(rs) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	st, err := tx.Prepare(`INSERT INTO baselines VALUES (?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT DO UPDATE SET
		n=excluded.n, mean=excluded.mean, var=excluded.var, short=excluded.short, svar=excluded.svar, first=excluded.first, last=excluded.last`)
	if err != nil {
		return err
	}
	defer st.Close()
	for _, r := range rs {
		x := r.stat
		if _, err := st.Exec(r.company, r.plant, r.asset, r.field, r.state, x.N, x.Mean, x.Var, x.Short, x.ShortVar, x.First, x.Last); err != nil {
			return err
		}
	}
	// a hint is kept for 90 days
	if _, err := tx.Exec(`DELETE FROM hints WHERE since < ?`, time.Now().Add(-90*24*time.Hour).UnixMilli()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) forget(company, plant, asset string) error {
	_, err := s.db.Exec(`DELETE FROM baselines WHERE company=? AND plant=? AND asset=?`, company, plant, asset)
	return err
}

func (s *Store) lastID() (int64, error) {
	var id sql.NullInt64
	err := s.db.QueryRow(`SELECT max(id) FROM hints`).Scan(&id)
	return id.Int64, err
}

func (s *Store) add(a Anomaly) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO hints VALUES (?,?,?,?,?,?,?,?,NULL,?,?,?,?,?,?,?)`, a.ID, a.Company, a.Plant, a.Asset, a.Field, a.State, a.Kind,
		a.Since.UnixMilli(), a.Value, a.Z, a.Peak, a.Mean, a.Sigma, a.Source, a.Text)
	return err
}

func (s *Store) end(a Anomaly) error {
	var ended int64
	if a.Ended != nil {
		ended = a.Ended.UnixMilli()
	}
	_, err := s.db.Exec(`UPDATE hints SET ended=?, kind=?, value=?, z=?, peak=? WHERE id=?`, ended, a.Kind, a.Value, a.Z, a.Peak, a.ID)
	return err
}

func (s *Store) recent(company string, since time.Time, limit int) ([]Anomaly, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT id, company, plant, asset, field, state, kind, since, ended, value, z, peak, mean, sigma, source, text FROM hints
		WHERE company=? AND since >= ? ORDER BY since DESC LIMIT ?`, company, since.UnixMilli(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Anomaly{}
	for rows.Next() {
		var a Anomaly
		var since int64
		var ended sql.NullInt64
		if err := rows.Scan(&a.ID, &a.Company, &a.Plant, &a.Asset, &a.Field, &a.State, &a.Kind, &since, &ended, &a.Value, &a.Z, &a.Peak, &a.Mean, &a.Sigma, &a.Source, &a.Text); err != nil {
			return nil, err
		}
		a.Since = time.UnixMilli(since)
		if ended.Valid {
			t := time.UnixMilli(ended.Int64)
			a.Ended = &t
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
