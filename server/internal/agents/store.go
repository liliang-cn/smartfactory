// Package agents runs a company's own assistants on agent-go: one made from a template (a
// document assistant, an R&D notebook, after-sales service), named and instructed by the
// company, working on what the company keeps here — its documents, notes and service tickets —
// and on the twin's live machines.
package agents

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// ErrNotFound is returned for an id that is not there (or belongs to another company).
var ErrNotFound = errors.New("not found")

type Agent struct {
	ID           string    `json:"id"`
	Company      string    `json:"company"`
	Kind         string    `json:"kind"`
	Name         string    `json:"name"`
	Instructions string    `json:"instructions"`
	Created      time.Time `json:"created"`
	Updated      time.Time `json:"updated"`
}

type Doc struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Size    int       `json:"size"`
	Parts   int       `json:"parts"`
	By      string    `json:"by"`
	Created time.Time `json:"created"`
}

type Note struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Body    string    `json:"body"`
	Tags    []string  `json:"tags"`
	Assets  []string  `json:"assets"`
	By      string    `json:"by"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
}

type Ticket struct {
	ID       string      `json:"id"`
	Customer string      `json:"customer"`
	Product  string      `json:"product"`
	Batch    string      `json:"batch"`
	Title    string      `json:"title"`
	Detail   string      `json:"detail"`
	Severity string      `json:"severity"`
	Status   string      `json:"status"`
	Reply    string      `json:"reply"`
	Log      []TicketLog `json:"log"`
	Created  time.Time   `json:"created"`
	Updated  time.Time   `json:"updated"`
}

type TicketLog struct {
	At   time.Time `json:"at"`
	By   string    `json:"by"`
	Text string    `json:"text"`
}

var (
	Severities = []string{"高", "中", "低"}
	Statuses   = []string{"待处理", "处理中", "待客户确认", "已关闭"}
)

type Conversation struct {
	ID      string    `json:"id"`
	Agent   string    `json:"agent"`
	Title   string    `json:"title"`
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
}

// Step is one tool call of an answer, kept so the conversation can be read back.
type Step struct {
	Tool   string         `json:"tool"`
	Args   map[string]any `json:"args,omitempty"`
	Result any            `json:"result,omitempty"`
}

type Message struct {
	ID     int64     `json:"id"`
	Role   string    `json:"role"` // user | assistant
	Text   string    `json:"text"`
	Steps  []Step    `json:"steps,omitempty"`
	Status string    `json:"status"` // done | running | stopped | failed
	At     time.Time `json:"at"`
}

type Store struct{ db *sql.DB }

// Open opens (creating) the store in dir.
func Open(dir string) (*Store, error) {
	db, err := sql.Open("sqlite", filepath.Join(dir, "agents.db")+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS agents (id TEXT PRIMARY KEY, company TEXT NOT NULL, kind TEXT NOT NULL, name TEXT NOT NULL,
	instructions TEXT NOT NULL DEFAULT '', created TEXT NOT NULL, updated TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS docs (id TEXT PRIMARY KEY, company TEXT NOT NULL, title TEXT NOT NULL, body TEXT NOT NULL,
	by TEXT NOT NULL, created TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS chunks (doc TEXT NOT NULL REFERENCES docs(id) ON DELETE CASCADE, seq INTEGER NOT NULL,
	heading TEXT NOT NULL, text TEXT NOT NULL, PRIMARY KEY (doc, seq));
CREATE TABLE IF NOT EXISTS notes (id TEXT PRIMARY KEY, company TEXT NOT NULL, title TEXT NOT NULL, body TEXT NOT NULL,
	tags TEXT NOT NULL, assets TEXT NOT NULL, by TEXT NOT NULL, created TEXT NOT NULL, updated TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS tickets (id TEXT NOT NULL, company TEXT NOT NULL, customer TEXT NOT NULL, product TEXT NOT NULL,
	batch TEXT NOT NULL, title TEXT NOT NULL, detail TEXT NOT NULL, severity TEXT NOT NULL, status TEXT NOT NULL,
	reply TEXT NOT NULL, log TEXT NOT NULL, created TEXT NOT NULL, updated TEXT NOT NULL, PRIMARY KEY (company, id));
CREATE TABLE IF NOT EXISTS conversations (id TEXT PRIMARY KEY, company TEXT NOT NULL, agent TEXT NOT NULL REFERENCES agents(id) ON DELETE CASCADE,
	owner TEXT NOT NULL, title TEXT NOT NULL, created TEXT NOT NULL, updated TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS messages (id INTEGER PRIMARY KEY AUTOINCREMENT, conv TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
	role TEXT NOT NULL, text TEXT NOT NULL, steps TEXT NOT NULL DEFAULT 'null', status TEXT NOT NULL, at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS seeded (company TEXT PRIMARY KEY);
CREATE INDEX IF NOT EXISTS conv_owner ON conversations(company, agent, owner, updated);
`); err != nil {
		db.Close()
		return nil, err
	}
	// an answer that was being written when the server stopped will never finish
	if _, err := db.Exec(`UPDATE messages SET status='stopped' WHERE status='running'`); err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func newID() string {
	b := make([]byte, 8)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func ts(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
func parseTS(v string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, v)
	return t
}

func jsonText(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// ---- agents ----

func (s *Store) Agents(company string) ([]Agent, error) {
	rows, err := s.db.Query(`SELECT id, company, kind, name, instructions, created, updated FROM agents WHERE company=? ORDER BY created`, company)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Agent{}
	for rows.Next() {
		var a Agent
		var c, u string
		if err := rows.Scan(&a.ID, &a.Company, &a.Kind, &a.Name, &a.Instructions, &c, &u); err != nil {
			return nil, err
		}
		a.Created, a.Updated = parseTS(c), parseTS(u)
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) Agent(company, id string) (Agent, error) {
	var a Agent
	var c, u string
	err := s.db.QueryRow(`SELECT id, company, kind, name, instructions, created, updated FROM agents WHERE company=? AND id=?`, company, id).
		Scan(&a.ID, &a.Company, &a.Kind, &a.Name, &a.Instructions, &c, &u)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	a.Created, a.Updated = parseTS(c), parseTS(u)
	return a, err
}

func (s *Store) CreateAgent(company, kind, name, instructions string) (Agent, error) {
	now := time.Now()
	a := Agent{ID: newID(), Company: company, Kind: kind, Name: name, Instructions: instructions, Created: now, Updated: now}
	_, err := s.db.Exec(`INSERT INTO agents VALUES (?,?,?,?,?,?,?)`, a.ID, company, kind, name, instructions, ts(now), ts(now))
	return a, err
}

func (s *Store) UpdateAgent(company, id, name, instructions string) (Agent, error) {
	res, err := s.db.Exec(`UPDATE agents SET name=?, instructions=?, updated=? WHERE company=? AND id=?`, name, instructions, ts(time.Now()), company, id)
	if err != nil {
		return Agent{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Agent{}, ErrNotFound
	}
	return s.Agent(company, id)
}

func (s *Store) DeleteAgent(company, id string) error {
	return affected(s.db.Exec(`DELETE FROM agents WHERE company=? AND id=?`, company, id))
}

func affected(res sql.Result, err error) error {
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ---- documents ----

// AddDoc keeps a document and cuts it into parts by its headings, for searching.
func (s *Store) AddDoc(company, title, body, by string) (Doc, error) {
	parts := chunk(body)
	d := Doc{ID: newID(), Title: title, Size: len([]rune(body)), Parts: len(parts), By: by, Created: time.Now()}
	tx, err := s.db.Begin()
	if err != nil {
		return d, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO docs VALUES (?,?,?,?,?,?)`, d.ID, company, title, body, by, ts(d.Created)); err != nil {
		return d, err
	}
	for i, p := range parts {
		if _, err := tx.Exec(`INSERT INTO chunks VALUES (?,?,?,?)`, d.ID, i, p.heading, p.text); err != nil {
			return d, err
		}
	}
	return d, tx.Commit()
}

func (s *Store) Docs(company string) ([]Doc, error) {
	rows, err := s.db.Query(`SELECT d.id, d.title, length(d.body), (SELECT count(*) FROM chunks c WHERE c.doc=d.id), d.by, d.created
		FROM docs d WHERE d.company=? ORDER BY d.created DESC`, company)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Doc{}
	for rows.Next() {
		var d Doc
		var c string
		if err := rows.Scan(&d.ID, &d.Title, &d.Size, &d.Parts, &d.By, &c); err != nil {
			return nil, err
		}
		d.Created = parseTS(c)
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) DocBody(company, id string) (Doc, string, error) {
	var d Doc
	var body, c string
	err := s.db.QueryRow(`SELECT id, title, body, by, created FROM docs WHERE company=? AND id=?`, company, id).Scan(&d.ID, &d.Title, &body, &d.By, &c)
	if errors.Is(err, sql.ErrNoRows) {
		return d, "", ErrNotFound
	}
	d.Created, d.Size = parseTS(c), len([]rune(body))
	return d, body, err
}

func (s *Store) DeleteDoc(company, id string) error {
	return affected(s.db.Exec(`DELETE FROM docs WHERE company=? AND id=?`, company, id))
}

// Part is one searchable piece of a document.
type Part struct {
	Doc     string `json:"doc"`
	Title   string `json:"title"`
	Seq     int    `json:"part"`
	Heading string `json:"heading"`
	Text    string `json:"text"`
}

func (s *Store) parts(company string) ([]Part, error) {
	rows, err := s.db.Query(`SELECT c.doc, d.title, c.seq, c.heading, c.text FROM chunks c JOIN docs d ON d.id=c.doc WHERE d.company=? ORDER BY d.created, c.seq`, company)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Part
	for rows.Next() {
		var p Part
		if err := rows.Scan(&p.Doc, &p.Title, &p.Seq, &p.Heading, &p.Text); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// SearchDocs finds the parts of the company's documents that best match query.
func (s *Store) SearchDocs(company, query string, limit int) ([]Part, error) {
	all, err := s.parts(company)
	if err != nil {
		return nil, err
	}
	return rank(all, query, limit, func(p Part) string { return p.Title + "\n" + p.Heading + "\n" + p.Text }), nil
}

// ReadDoc returns parts from..from+n of one document.
func (s *Store) ReadDoc(company, id string, from, n int) ([]Part, int, error) {
	all, err := s.parts(company)
	if err != nil {
		return nil, 0, err
	}
	var mine []Part
	for _, p := range all {
		if p.Doc == id {
			mine = append(mine, p)
		}
	}
	if len(mine) == 0 {
		return nil, 0, ErrNotFound
	}
	from = max(0, min(from, len(mine)-1))
	return mine[from:min(len(mine), from+n)], len(mine), nil
}

// ---- notes ----

func scanNote(sc interface{ Scan(...any) error }) (Note, error) {
	var n Note
	var tags, assets, c, u string
	if err := sc.Scan(&n.ID, &n.Title, &n.Body, &tags, &assets, &n.By, &c, &u); err != nil {
		return n, err
	}
	json.Unmarshal([]byte(tags), &n.Tags)
	json.Unmarshal([]byte(assets), &n.Assets)
	if n.Tags == nil {
		n.Tags = []string{}
	}
	if n.Assets == nil {
		n.Assets = []string{}
	}
	n.Created, n.Updated = parseTS(c), parseTS(u)
	return n, nil
}

const noteCols = `id, title, body, tags, assets, by, created, updated`

func (s *Store) Notes(company string) ([]Note, error) {
	rows, err := s.db.Query(`SELECT `+noteCols+` FROM notes WHERE company=? ORDER BY updated DESC`, company)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Note{}
	for rows.Next() {
		n, err := scanNote(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) Note(company, id string) (Note, error) {
	n, err := scanNote(s.db.QueryRow(`SELECT `+noteCols+` FROM notes WHERE company=? AND id=?`, company, id))
	if errors.Is(err, sql.ErrNoRows) {
		return n, ErrNotFound
	}
	return n, err
}

func (s *Store) SaveNote(company string, n Note) (Note, error) {
	now := time.Now()
	if n.Tags == nil {
		n.Tags = []string{}
	}
	if n.Assets == nil {
		n.Assets = []string{}
	}
	if n.ID == "" {
		n.ID, n.Created = newID(), now
		if !n.Updated.IsZero() {
			n.Created = n.Updated
		} else {
			n.Updated = now
		}
		_, err := s.db.Exec(`INSERT INTO notes VALUES (?,?,?,?,?,?,?,?,?)`, n.ID, company, n.Title, n.Body, jsonText(n.Tags), jsonText(n.Assets), n.By, ts(n.Created), ts(n.Updated))
		return n, err
	}
	n.Updated = now
	if err := affected(s.db.Exec(`UPDATE notes SET title=?, body=?, tags=?, assets=?, updated=? WHERE company=? AND id=?`,
		n.Title, n.Body, jsonText(n.Tags), jsonText(n.Assets), ts(now), company, n.ID)); err != nil {
		return n, err
	}
	return s.Note(company, n.ID)
}

func (s *Store) DeleteNote(company, id string) error {
	return affected(s.db.Exec(`DELETE FROM notes WHERE company=? AND id=?`, company, id))
}

func (s *Store) SearchNotes(company, query string, limit int) ([]Note, error) {
	all, err := s.Notes(company)
	if err != nil {
		return nil, err
	}
	return rank(all, query, limit, func(n Note) string {
		return n.Title + "\n" + strings.Join(n.Tags, " ") + " " + strings.Join(n.Assets, " ") + "\n" + n.Body
	}), nil
}

// ---- tickets ----

const ticketCols = `id, customer, product, batch, title, detail, severity, status, reply, log, created, updated`

func scanTicket(sc interface{ Scan(...any) error }) (Ticket, error) {
	var t Ticket
	var lg, c, u string
	if err := sc.Scan(&t.ID, &t.Customer, &t.Product, &t.Batch, &t.Title, &t.Detail, &t.Severity, &t.Status, &t.Reply, &lg, &c, &u); err != nil {
		return t, err
	}
	json.Unmarshal([]byte(lg), &t.Log)
	if t.Log == nil {
		t.Log = []TicketLog{}
	}
	t.Created, t.Updated = parseTS(c), parseTS(u)
	return t, nil
}

func (s *Store) Tickets(company string) ([]Ticket, error) {
	rows, err := s.db.Query(`SELECT `+ticketCols+` FROM tickets WHERE company=? ORDER BY created DESC`, company)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Ticket{}
	for rows.Next() {
		t, err := scanTicket(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) Ticket(company, id string) (Ticket, error) {
	t, err := scanTicket(s.db.QueryRow(`SELECT `+ticketCols+` FROM tickets WHERE company=? AND id=?`, company, id))
	if errors.Is(err, sql.ErrNoRows) {
		return t, ErrNotFound
	}
	return t, err
}

// CreateTicket numbers the ticket SV-{year}-{n} within the company.
func (s *Store) CreateTicket(company string, t Ticket) (Ticket, error) {
	if t.Created.IsZero() {
		t.Created = time.Now()
	}
	if t.Updated.IsZero() {
		t.Updated = t.Created
	}
	if t.Status == "" {
		t.Status = Statuses[0]
	}
	if t.Log == nil {
		t.Log = []TicketLog{}
	}
	prefix := fmt.Sprintf("SV-%d-", t.Created.Year())
	var n int
	s.db.QueryRow(`SELECT count(*) FROM tickets WHERE company=? AND id LIKE ?`, company, prefix+"%").Scan(&n)
	t.ID = fmt.Sprintf("%s%04d", prefix, n+1)
	_, err := s.db.Exec(`INSERT INTO tickets VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`, t.ID, company, t.Customer, t.Product, t.Batch, t.Title, t.Detail,
		t.Severity, t.Status, t.Reply, jsonText(t.Log), ts(t.Created), ts(t.Updated))
	return t, err
}

// UpdateTicket changes status and reply (when given) and appends what was done to the log.
func (s *Store) UpdateTicket(company, id, status, reply, by, note string) (Ticket, error) {
	t, err := s.Ticket(company, id)
	if err != nil {
		return t, err
	}
	now := time.Now()
	if status != "" {
		t.Status = status
	}
	if reply != "" {
		t.Reply = reply
	}
	if note != "" || status != "" {
		text := note
		if status != "" {
			text = strings.TrimSpace("状态改为「" + status + "」。" + note)
		}
		t.Log = append(t.Log, TicketLog{At: now, By: by, Text: text})
	}
	t.Updated = now
	_, err = s.db.Exec(`UPDATE tickets SET status=?, reply=?, log=?, updated=? WHERE company=? AND id=?`, t.Status, t.Reply, jsonText(t.Log), ts(now), company, id)
	return t, err
}

func (s *Store) DeleteTicket(company, id string) error {
	return affected(s.db.Exec(`DELETE FROM tickets WHERE company=? AND id=?`, company, id))
}

func (s *Store) SearchTickets(company, query, status string, limit int) ([]Ticket, error) {
	all, err := s.Tickets(company)
	if err != nil {
		return nil, err
	}
	if status != "" {
		var keep []Ticket
		for _, t := range all {
			if t.Status == status {
				keep = append(keep, t)
			}
		}
		all = keep
	}
	if strings.TrimSpace(query) == "" {
		return all[:min(len(all), limit)], nil
	}
	return rank(all, query, limit, func(t Ticket) string {
		return strings.Join([]string{t.ID, t.Customer, t.Product, t.Batch, t.Title, t.Detail, t.Reply}, "\n")
	}), nil
}

// ---- conversations ----

func (s *Store) Conversations(company, agent, owner string) ([]Conversation, error) {
	rows, err := s.db.Query(`SELECT id, agent, title, created, updated FROM conversations WHERE company=? AND agent=? AND owner=? ORDER BY updated DESC LIMIT 50`, company, agent, owner)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Conversation{}
	for rows.Next() {
		var c Conversation
		var cr, up string
		if err := rows.Scan(&c.ID, &c.Agent, &c.Title, &cr, &up); err != nil {
			return nil, err
		}
		c.Created, c.Updated = parseTS(cr), parseTS(up)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) Conversation(company, id, owner string) (Conversation, error) {
	var c Conversation
	var cr, up string
	err := s.db.QueryRow(`SELECT id, agent, title, created, updated FROM conversations WHERE company=? AND id=? AND owner=?`, company, id, owner).
		Scan(&c.ID, &c.Agent, &c.Title, &cr, &up)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	c.Created, c.Updated = parseTS(cr), parseTS(up)
	return c, err
}

func (s *Store) CreateConversation(company, agent, owner, title string) (Conversation, error) {
	now := time.Now()
	c := Conversation{ID: newID(), Agent: agent, Title: title, Created: now, Updated: now}
	_, err := s.db.Exec(`INSERT INTO conversations VALUES (?,?,?,?,?,?,?)`, c.ID, company, agent, owner, title, ts(now), ts(now))
	return c, err
}

func (s *Store) DeleteConversation(company, id, owner string) error {
	return affected(s.db.Exec(`DELETE FROM conversations WHERE company=? AND id=? AND owner=?`, company, id, owner))
}

func (s *Store) Messages(conv string) ([]Message, error) {
	rows, err := s.db.Query(`SELECT id, role, text, steps, status, at FROM messages WHERE conv=? ORDER BY id`, conv)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Message{}
	for rows.Next() {
		var m Message
		var steps, at string
		if err := rows.Scan(&m.ID, &m.Role, &m.Text, &steps, &m.Status, &at); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(steps), &m.Steps)
		m.At = parseTS(at)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) AddMessage(conv string, m Message) (int64, error) {
	if m.At.IsZero() {
		m.At = time.Now()
	}
	res, err := s.db.Exec(`INSERT INTO messages (conv, role, text, steps, status, at) VALUES (?,?,?,?,?,?)`, conv, m.Role, m.Text, jsonText(m.Steps), m.Status, ts(m.At))
	if err != nil {
		return 0, err
	}
	s.db.Exec(`UPDATE conversations SET updated=? WHERE id=?`, ts(m.At), conv)
	return res.LastInsertId()
}

func (s *Store) FinishMessage(id int64, text string, steps []Step, status string) error {
	_, err := s.db.Exec(`UPDATE messages SET text=?, steps=?, status=? WHERE id=?`, text, jsonText(steps), status, id)
	return err
}

// ---- seeding ----

// seed runs fill once per company, the first time the company's assistants are looked at.
func (s *Store) seed(company string, fill func() error) error {
	res, err := s.db.Exec(`INSERT OR IGNORE INTO seeded VALUES (?)`, company)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil
	}
	if err := fill(); err != nil {
		s.db.Exec(`DELETE FROM seeded WHERE company=?`, company)
		return err
	}
	return nil
}
