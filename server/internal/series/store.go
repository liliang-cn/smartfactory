// Package series keeps every reading equipment sends, in {data}/series.db, so a machine's
// temperature last Tuesday can be looked at and an advisor or an assistant can reason over what
// really happened rather than the simulation.
//
// It is SQLite (the server's other stores are too, and it needs no service of its own), but
// readings are not one row each: a busy plant sends hundreds a second, and rows would cost
// tens of bytes apiece. New readings land in a plain table (tail); once an hour is over, each
// series' readings of that hour are sealed into one compressed chunk (time deltas, XORed values,
// deflate — a few bytes a reading), and its one-minute and one-hour aggregates are written the
// same way. Raw chunks are kept for Raw, minutes for Minute, hours for Hour; MaxMB bounds the
// file, dropping the oldest raw chunks first, then the oldest minutes. A reading that arrives
// after its hour was sealed (a gateway replaying what it buffered) is merged in at the next pass.
package series

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"

	"smartfactory/internal/config"
	"smartfactory/internal/tags"
)

const (
	minute = int64(time.Minute / time.Millisecond)
	hour   = int64(time.Hour / time.Millisecond)
	day    = 24 * hour
)

// Retention is how long each resolution is kept and how big the store may grow.
type Retention struct {
	Raw, Minute, Hour time.Duration
	MaxBytes          int64
}

// RetentionFrom fills a config's gaps with the defaults: 14 days raw, 90 days of minutes,
// 5 years of hours, 4 GB.
func RetentionFrom(c config.Series) Retention {
	r := Retention{Raw: c.Raw.Or(14 * 24 * time.Hour), Minute: c.Minute.Or(90 * 24 * time.Hour), Hour: c.Hour.Or(5 * 365 * 24 * time.Hour), MaxBytes: 4 << 30}
	if c.MaxMB > 0 {
		r.MaxBytes = int64(c.MaxMB) << 20
	}
	return r
}

// Store is the series database.
type Store struct {
	db  *sql.DB
	log *slog.Logger
	ret Retention

	in      chan tags.Tag
	dropped atomic.Uint64
	stored  atomic.Uint64
	wg      sync.WaitGroup
	once    sync.Once

	mu      sync.Mutex
	ids     map[string]int64  // company/plant/asset/field → series id
	srcs    map[string]uint32 // source name → id
	srcName map[uint32]string
	states  map[int64]string // last state stored per series, to keep changes only

	// Now is the clock (tests set it).
	Now func() time.Time
	// Grace is how long after an hour ends it is sealed: readings a little late still land in
	// the tail.
	Grace time.Duration
}

func Open(dir string, ret Retention, log *slog.Logger) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "series.db")+"?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=auto_vacuum(INCREMENTAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1) // one writer; reads queue behind short transactions
	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS series (id INTEGER PRIMARY KEY, company TEXT NOT NULL, plant TEXT NOT NULL, asset TEXT NOT NULL, field TEXT NOT NULL,
			UNIQUE (company, plant, asset, field));
		CREATE TABLE IF NOT EXISTS sources (id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE);
		CREATE TABLE IF NOT EXISTS tail (sid INTEGER NOT NULL, t INTEGER NOT NULL, v REAL NOT NULL, q INTEGER NOT NULL, src INTEGER NOT NULL);
		CREATE INDEX IF NOT EXISTS tail_sid_t ON tail (sid, t);
		CREATE TABLE IF NOT EXISTS raw (sid INTEGER NOT NULL, hour INTEGER NOT NULL, n INTEGER NOT NULL, data BLOB NOT NULL, PRIMARY KEY (sid, hour)) WITHOUT ROWID;
		CREATE INDEX IF NOT EXISTS raw_hour ON raw (hour);
		CREATE TABLE IF NOT EXISTS minutes (sid INTEGER NOT NULL, hour INTEGER NOT NULL, data BLOB NOT NULL, PRIMARY KEY (sid, hour)) WITHOUT ROWID;
		CREATE INDEX IF NOT EXISTS minutes_hour ON minutes (hour);
		CREATE TABLE IF NOT EXISTS hours (sid INTEGER NOT NULL, day INTEGER NOT NULL, data BLOB NOT NULL, PRIMARY KEY (sid, day)) WITHOUT ROWID;
		CREATE TABLE IF NOT EXISTS events (sid INTEGER NOT NULL, t INTEGER NOT NULL, v TEXT NOT NULL, q INTEGER NOT NULL, src INTEGER NOT NULL);
		CREATE INDEX IF NOT EXISTS events_sid_t ON events (sid, t);`); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db, log: log, ret: ret, in: make(chan tags.Tag, 1<<16), ids: map[string]int64{}, srcs: map[string]uint32{}, srcName: map[uint32]string{},
		states: map[int64]string{}, Now: time.Now, Grace: 2 * time.Minute}
	if err := s.load(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	rows, err := s.db.Query(`SELECT id, company, plant, asset, field FROM series`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		var c, p, a, f string
		rows.Scan(&id, &c, &p, &a, &f)
		s.ids[c+"/"+p+"/"+a+"/"+f] = id
	}
	rows.Close()
	rows, err = s.db.Query(`SELECT id, name FROM sources`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var id uint32
		var n string
		rows.Scan(&id, &n)
		s.srcs[n], s.srcName[id] = id, n
	}
	rows.Close()
	// the last state of each series, so a restart does not store it again
	rows, err = s.db.Query(`SELECT e.sid, e.v FROM events e JOIN (SELECT sid, max(t) t FROM events GROUP BY sid) m ON e.sid = m.sid AND e.t = m.t`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var v string
		rows.Scan(&id, &v)
		s.states[id] = v
	}
	return rows.Err()
}

// Add takes a reading; it never blocks (when the writer falls behind, readings are dropped and
// counted).
func (s *Store) Add(t tags.Tag) {
	select {
	case s.in <- t:
	default:
		s.dropped.Add(1)
	}
}

// Stats is how the store is doing.
type Stats struct {
	Stored  uint64 `json:"stored"`
	Dropped uint64 `json:"dropped"`
	Series  int    `json:"series"`
	Bytes   int64  `json:"bytes"`
}

func (s *Store) Stats() Stats {
	s.mu.Lock()
	n := len(s.ids)
	s.mu.Unlock()
	return Stats{Stored: s.stored.Load(), Dropped: s.dropped.Load(), Series: n, Bytes: s.size()}
}

// Run writes readings as they come and seals, ages and trims the store in the background until
// ctx ends; then it writes what is left.
func (s *Store) Run(ctx context.Context) {
	s.wg.Add(1)
	defer s.wg.Done()
	flush := time.NewTicker(time.Second)
	defer flush.Stop()
	upkeep := time.NewTicker(time.Minute)
	defer upkeep.Stop()
	var batch []tags.Tag
	write := func() {
		if len(batch) == 0 {
			return
		}
		if err := s.write(batch); err != nil {
			s.log.Error("series: write", "err", err, "readings", len(batch))
		}
		batch = batch[:0]
	}
	s.Upkeep()
	for {
		select {
		case <-ctx.Done():
			for {
				select {
				case t := <-s.in:
					batch = append(batch, t)
					continue
				default:
				}
				break
			}
			write()
			return
		case t := <-s.in:
			batch = append(batch, t)
			if len(batch) >= 4096 {
				write()
			}
		case <-flush.C:
			write()
		case <-upkeep.C:
			write()
			s.Upkeep()
		}
	}
}

// Close waits for Run to finish, then closes the database.
func (s *Store) Close() error {
	s.wg.Wait()
	return s.db.Close()
}

// Flush writes what is waiting (tests).
func (s *Store) Flush() error {
	var batch []tags.Tag
	for {
		select {
		case t := <-s.in:
			batch = append(batch, t)
			continue
		default:
		}
		break
	}
	return s.write(batch)
}

func number(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(x), 64); err == nil {
			return f, true
		}
	}
	if n, ok := v.(interface{ Float64() (float64, error) }); ok {
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func (s *Store) write(batch []tags.Tag) error {
	if len(batch) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ins, err := tx.Prepare(`INSERT INTO tail VALUES (?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer ins.Close()
	ev, err := tx.Prepare(`INSERT INTO events VALUES (?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer ev.Close()
	n := 0
	for _, t := range batch {
		sid, err := s.seriesID(tx, t.Company, t.Plant, t.Asset, t.Field)
		if err != nil {
			return err
		}
		src, err := s.sourceID(tx, t.Source)
		if err != nil {
			return err
		}
		at := t.At.UnixMilli()
		if t.At.IsZero() {
			at = s.Now().UnixMilli()
		}
		if v, ok := number(t.Value); ok {
			if _, err := ins.Exec(sid, at, v, t.Q, src); err != nil {
				return err
			}
			n++
			continue
		}
		// a state word, a fault text: kept when it changes
		str := strings.TrimSpace(fmt.Sprint(t.Value))
		s.mu.Lock()
		same := s.states[sid] == str
		s.states[sid] = str
		s.mu.Unlock()
		if same {
			continue
		}
		if _, err := ev.Exec(sid, at, str, t.Q, src); err != nil {
			return err
		}
		n++
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.stored.Add(uint64(n))
	return nil
}

func (s *Store) seriesID(tx *sql.Tx, c, p, a, f string) (int64, error) {
	k := c + "/" + p + "/" + a + "/" + f
	s.mu.Lock()
	id, ok := s.ids[k]
	s.mu.Unlock()
	if ok {
		return id, nil
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO series (company, plant, asset, field) VALUES (?,?,?,?)`, c, p, a, f); err != nil {
		return 0, err
	}
	if err := tx.QueryRow(`SELECT id FROM series WHERE company=? AND plant=? AND asset=? AND field=?`, c, p, a, f).Scan(&id); err != nil {
		return 0, err
	}
	s.mu.Lock()
	s.ids[k] = id
	s.mu.Unlock()
	return id, nil
}

func (s *Store) sourceID(tx *sql.Tx, name string) (uint32, error) {
	s.mu.Lock()
	id, ok := s.srcs[name]
	s.mu.Unlock()
	if ok {
		return id, nil
	}
	if _, err := tx.Exec(`INSERT OR IGNORE INTO sources (name) VALUES (?)`, name); err != nil {
		return 0, err
	}
	if err := tx.QueryRow(`SELECT id FROM sources WHERE name=?`, name).Scan(&id); err != nil {
		return 0, err
	}
	s.mu.Lock()
	s.srcs[name], s.srcName[id] = id, name
	s.mu.Unlock()
	return id, nil
}

// Upkeep seals finished hours, drops what is past its retention, and trims the store to its
// size. It runs every minute; tests call it directly.
func (s *Store) Upkeep() {
	if err := s.seal(); err != nil {
		s.log.Error("series: seal", "err", err)
	}
	if err := s.age(); err != nil {
		s.log.Error("series: retention", "err", err)
	}
	if err := s.trim(); err != nil {
		s.log.Error("series: size", "err", err)
	}
}

// seal moves the tail's readings of finished hours into compressed chunks and writes their
// minute and hour aggregates, merging with what an earlier pass sealed.
func (s *Store) seal() error {
	cutoff := (s.Now().Add(-s.Grace).UnixMilli()) / hour * hour
	rows, err := s.db.Query(`SELECT DISTINCT sid, t / ? * ? FROM tail WHERE t < ?`, hour, hour, cutoff)
	if err != nil {
		return err
	}
	type key struct{ sid, hour int64 }
	var todo []key
	for rows.Next() {
		var k key
		rows.Scan(&k.sid, &k.hour)
		todo = append(todo, k)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for start := 0; start < len(todo); start += 200 {
		part := todo[start:min(start+200, len(todo))]
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		for _, k := range part {
			if err := s.sealHour(tx, k.sid, k.hour); err != nil {
				tx.Rollback()
				return err
			}
		}
		for _, k := range part {
			if _, err := tx.Exec(`DELETE FROM tail WHERE sid=? AND t >= ? AND t < ?`, k.sid, k.hour, k.hour+hour); err != nil {
				tx.Rollback()
				return err
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) sealHour(tx *sql.Tx, sid, h int64) error {
	rows, err := tx.Query(`SELECT t, v, q, src FROM tail WHERE sid=? AND t >= ? AND t < ? ORDER BY t`, sid, h, h+hour)
	if err != nil {
		return err
	}
	var fresh []Point
	for rows.Next() {
		var p Point
		rows.Scan(&p.T, &p.V, &p.Q, &p.Src)
		fresh = append(fresh, p)
	}
	rows.Close()
	var old []byte
	err = tx.QueryRow(`SELECT data FROM raw WHERE sid=? AND hour=?`, sid, h).Scan(&old)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var prev []Point
	if old != nil {
		if prev, err = decodePoints(h, old); err != nil {
			return err
		}
	}
	all := mergePoints(prev, fresh)
	if h >= s.Now().Add(-s.ret.Raw).UnixMilli()/hour*hour {
		if _, err := tx.Exec(`INSERT OR REPLACE INTO raw VALUES (?,?,?,?)`, sid, h, len(all), encodePoints(h, all)); err != nil {
			return err
		}
	}
	mins := aggregate(h, minute, all, tags.Doubtful)
	if old == nil {
		// the hour's raw chunk is gone (aged out) but its minutes may not be: add to them
		var mb []byte
		err := tx.QueryRow(`SELECT data FROM minutes WHERE sid=? AND hour=?`, sid, h).Scan(&mb)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if mb != nil {
			had, err := decodeAggs(h, minute, mb)
			if err != nil {
				return err
			}
			for _, m := range mins {
				i := indexAgg(had, m.T)
				if i >= 0 {
					m = combine(m.T, []Agg{had[i], m})
				}
				had = putAgg(had, m)
			}
			mins = had
		}
	}
	if _, err := tx.Exec(`INSERT OR REPLACE INTO minutes VALUES (?,?,?)`, sid, h, encodeAggs(h, minute, mins)); err != nil {
		return err
	}
	d := h / day * day
	var hb []byte
	err = tx.QueryRow(`SELECT data FROM hours WHERE sid=? AND day=?`, sid, d).Scan(&hb)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var hs []Agg
	if hb != nil {
		if hs, err = decodeAggs(d, hour, hb); err != nil {
			return err
		}
	}
	hs = putAgg(hs, combine(h, mins))
	_, err = tx.Exec(`INSERT OR REPLACE INTO hours VALUES (?,?,?)`, sid, d, encodeAggs(d, hour, hs))
	return err
}

// age drops raw chunks, minutes, hours and state changes past their retention.
func (s *Store) age() error {
	now := s.Now()
	for _, q := range []struct {
		sql string
		arg int64
	}{
		{`DELETE FROM raw WHERE hour < ?`, now.Add(-s.ret.Raw).UnixMilli()},
		{`DELETE FROM minutes WHERE hour < ?`, now.Add(-s.ret.Minute).UnixMilli()},
		{`DELETE FROM hours WHERE day < ?`, now.Add(-s.ret.Hour).UnixMilli()/day*day - day},
		{`DELETE FROM events WHERE t < ?`, now.Add(-s.ret.Minute).UnixMilli()},
	} {
		if _, err := s.db.Exec(q.sql, q.arg); err != nil {
			return err
		}
	}
	_, err := s.db.Exec(`PRAGMA incremental_vacuum`)
	return err
}

// size is the bytes the store's data takes (free pages not counted).
func (s *Store) size() int64 {
	var pages, free, ps int64
	s.db.QueryRow(`PRAGMA page_count`).Scan(&pages)
	s.db.QueryRow(`PRAGMA freelist_count`).Scan(&free)
	s.db.QueryRow(`PRAGMA page_size`).Scan(&ps)
	return (pages - free) * ps
}

// trim keeps the store under its size: the oldest raw hours go first, then the oldest minutes.
func (s *Store) trim() error {
	if s.ret.MaxBytes <= 0 || s.size() <= s.ret.MaxBytes {
		return nil
	}
	goal := s.ret.MaxBytes * 9 / 10
	for _, t := range []struct{ table, col string }{{"raw", "hour"}, {"minutes", "hour"}} {
		for s.size() > goal {
			var oldest sql.NullInt64
			if err := s.db.QueryRow(`SELECT min(` + t.col + `) FROM ` + t.table).Scan(&oldest); err != nil {
				return err
			}
			if !oldest.Valid {
				break
			}
			// a day at a time: fewer passes, and the oldest go together
			if _, err := s.db.Exec(`DELETE FROM `+t.table+` WHERE `+t.col+` < ?`, oldest.Int64+day); err != nil {
				return err
			}
			s.log.Warn("series: over its size, dropped the oldest", "table", t.table, "before", time.UnixMilli(oldest.Int64+day).Format(time.DateTime))
		}
	}
	_, err := s.db.Exec(`PRAGMA incremental_vacuum`)
	return err
}
