// Package audit keeps who changed what: commands that may reach equipment, scenes, documents,
// notes, tickets and the assistants themselves, from the page, the assistants and MCP alike.
// Visitors' demo commands are not kept: they reach only emulated equipment and undo themselves.
package audit

import (
	"context"
	"database/sql"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"

	"smartfactory/internal/auth"
)

// Entry is one change.
type Entry struct {
	ID   int64     `json:"id"`
	At   time.Time `json:"at"`
	Who  string    `json:"who"`
	Role auth.Role `json:"role"`
	// Key names the token used (the start of its hash).
	Key string `json:"key"`
	// Via is where it came from: page | live | assistant | mcp
	Via     string `json:"via"`
	Company string `json:"company"`
	// Action is what was done, e.g. command, ticket.status, doc.add
	Action string `json:"action"`
	// Target is what it was done to: an asset, a ticket, a document …
	Target string `json:"target"`
	Detail string `json:"detail,omitempty"`
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
}

// Log is the audit log. A nil *Log records nothing.
type Log struct {
	db  *sql.DB
	log *slog.Logger
}

func Open(dir string, log *slog.Logger) (*Log, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "audit.db")+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS audit (
		id INTEGER PRIMARY KEY AUTOINCREMENT, at INTEGER NOT NULL, who TEXT NOT NULL, role TEXT NOT NULL, key TEXT NOT NULL,
		via TEXT NOT NULL, company TEXT NOT NULL, action TEXT NOT NULL, target TEXT NOT NULL, detail TEXT NOT NULL,
		ok INTEGER NOT NULL, error TEXT NOT NULL);
		CREATE INDEX IF NOT EXISTS audit_company ON audit(company, id)`); err != nil {
		db.Close()
		return nil, err
	}
	if log == nil {
		log = slog.Default()
	}
	return &Log{db: db, log: log}, nil
}

func (l *Log) Close() error {
	if l == nil {
		return nil
	}
	return l.db.Close()
}

// Record keeps one change made by w. A failure to write is logged, never returned: the change
// has already happened.
func (l *Log) Record(w auth.Who, via, company, action, target, detail string, err error) {
	if l == nil {
		return
	}
	e := Entry{At: time.Now(), Who: w.Name, Role: w.Role, Key: w.Key, Via: via, Company: company, Action: action, Target: target, Detail: detail, OK: err == nil}
	if err != nil {
		e.Error = err.Error()
	}
	if _, xerr := l.db.Exec(`INSERT INTO audit (at, who, role, key, via, company, action, target, detail, ok, error) VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		e.At.UnixMilli(), e.Who, e.Role.String(), e.Key, e.Via, e.Company, e.Action, e.Target, e.Detail, e.OK, e.Error); xerr != nil {
		l.log.Error("audit", "err", xerr, "who", e.Who, "action", e.Action, "target", e.Target)
	}
}

// List is company's changes ("" for every company's), newest first: limit of them, before the
// entry with id before (0 for the newest).
func (l *Log) List(ctx context.Context, company string, before int64, limit int) ([]Entry, error) {
	out := []Entry{}
	if l == nil {
		return out, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if before <= 0 {
		before = 1<<63 - 1
	}
	q := `SELECT id, at, who, role, key, via, company, action, target, detail, ok, error FROM audit WHERE id < ?`
	args := []any{before}
	if company != "" {
		q += ` AND company = ?`
		args = append(args, company)
	}
	rows, err := l.db.QueryContext(ctx, q+` ORDER BY id DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var e Entry
		var at int64
		var role string
		if err := rows.Scan(&e.ID, &at, &e.Who, &role, &e.Key, &e.Via, &e.Company, &e.Action, &e.Target, &e.Detail, &e.OK, &e.Error); err != nil {
			return nil, err
		}
		e.At = time.UnixMilli(at)
		e.Role, _ = auth.ParseRole(role)
		out = append(out, e)
	}
	return out, rows.Err()
}
