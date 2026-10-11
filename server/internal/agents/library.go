package agents

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"smartfactory/internal/extract"
)

// The library keeps each document's original file and every version of it: a file uploaded
// again under the same title (or with the same file name) is the document's next version, and
// its passages replace the old ones.

// migrateLibrary adds what documents need for files and versions to a store made before them.
func migrateLibrary(db *sql.DB) error {
	have := map[string]bool{}
	rows, err := db.Query(`SELECT name FROM pragma_table_info('docs')`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var n string
		rows.Scan(&n)
		have[n] = true
	}
	rows.Close()
	for _, c := range []struct{ name, def string }{
		{"kind", "TEXT NOT NULL DEFAULT 'markdown'"}, {"file", "TEXT NOT NULL DEFAULT ''"}, {"bytes", "INTEGER NOT NULL DEFAULT 0"},
		{"pages", "INTEGER NOT NULL DEFAULT 0"}, {"version", "INTEGER NOT NULL DEFAULT 1"}, {"problems", "TEXT NOT NULL DEFAULT '[]'"},
		{"updated", "TEXT NOT NULL DEFAULT ''"},
	} {
		if !have[c.name] {
			if _, err := db.Exec(`ALTER TABLE docs ADD COLUMN ` + c.name + ` ` + c.def); err != nil {
				return err
			}
		}
	}
	_, err = db.Exec(`
CREATE TABLE IF NOT EXISTS doc_versions (doc TEXT NOT NULL REFERENCES docs(id) ON DELETE CASCADE, version INTEGER NOT NULL,
	file TEXT NOT NULL, stored TEXT NOT NULL, kind TEXT NOT NULL, bytes INTEGER NOT NULL, pages INTEGER NOT NULL, parts INTEGER NOT NULL,
	problems TEXT NOT NULL, by TEXT NOT NULL, created TEXT NOT NULL, PRIMARY KEY (doc, version));
INSERT OR IGNORE INTO doc_versions SELECT d.id, d.version, d.file, '', d.kind, d.bytes, d.pages,
	(SELECT count(*) FROM chunks c WHERE c.doc=d.id), d.problems, d.by, CASE d.updated WHEN '' THEN d.created ELSE d.updated END FROM docs d;`)
	return err
}

type execer interface {
	Exec(string, ...any) (sql.Result, error)
}

// putChunks stores a document's passages (none for a document without text).
func putChunks(tx execer, doc string, parts []piece) error {
	if _, err := tx.Exec(`DELETE FROM chunks WHERE doc=?`, doc); err != nil {
		return err
	}
	seq := 0
	for _, p := range parts {
		if strings.TrimSpace(p.text) == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO chunks VALUES (?,?,?,?)`, doc, seq, p.heading, p.text); err != nil {
			return err
		}
		seq++
	}
	return nil
}

func addVersion(tx execer, d Doc, stored string) error {
	problems, _ := json.Marshal(nonNil(d.Problems))
	_, err := tx.Exec(`INSERT INTO doc_versions VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		d.ID, d.Version, d.File, stored, d.Kind, d.Bytes, d.Pages, d.Parts, string(problems), d.By, ts(d.Updated))
	return err
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// Upload is a file sent to the library, read by package extract.
type Upload struct {
	// Title is what the document is called; empty: the file's name without its extension.
	Title string
	File  string
	Data  []byte
	Doc   extract.Doc
	By    string
}

// Outcome is what an upload did: created a document, or a new version of one.
type Outcome string

const (
	Created Outcome = "created"
	Updated Outcome = "updated"
)

// TitleOf is the title a file is filed under when none is given.
func TitleOf(file string) string {
	t := strings.TrimSpace(strings.TrimSuffix(filepath.Base(file), filepath.Ext(file)))
	if r := []rune(t); len(r) > 60 {
		t = string(r[:60])
	}
	if t == "" {
		t = "未命名文档"
	}
	return t
}

// PutFile keeps an uploaded file: a new document, or the next version of the one with its file
// name, or with its title and of its kind (报告.pdf and 报告.txt stay two documents). It returns how many passages the replaced version had, for the knowledge base.
func (s *Store) PutFile(company string, u Upload) (Doc, Outcome, int, error) {
	if u.Title = strings.TrimSpace(u.Title); u.Title == "" {
		u.Title = TitleOf(u.File)
	}
	if utf8.RuneCountInString(u.Title) > 60 {
		return Doc{}, "", 0, errors.New("标题要 1～60 个字")
	}
	now := time.Now()
	parts := chunk(u.Doc.Body)
	if strings.TrimSpace(u.Doc.Body) == "" {
		parts = nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Doc{}, "", 0, err
	}
	defer tx.Rollback()
	var id string
	var version, before int
	err = tx.QueryRow(`SELECT id, version, (SELECT count(*) FROM chunks c WHERE c.doc=docs.id) FROM docs
		WHERE company=? AND ((title=? AND kind=?) OR (file<>'' AND file=?)) ORDER BY created LIMIT 1`, company, u.Title, u.Doc.Kind, filepath.Base(u.File)).Scan(&id, &version, &before)
	outcome := Updated
	if errors.Is(err, sql.ErrNoRows) {
		id, version, outcome = newID(), 0, Created
	} else if err != nil {
		return Doc{}, "", 0, err
	}
	d := Doc{ID: id, Title: u.Title, By: u.By, Kind: u.Doc.Kind, File: filepath.Base(u.File), Bytes: int64(len(u.Data)), Pages: u.Doc.Pages,
		Version: version + 1, Problems: u.Doc.Problems, Created: now, Updated: now, Size: utf8.RuneCountInString(u.Doc.Body)}
	stored, err := s.keep(company, d, u.Data)
	if err != nil {
		return Doc{}, "", 0, err
	}
	problems, _ := json.Marshal(nonNil(d.Problems))
	if outcome == Created {
		_, err = tx.Exec(`INSERT INTO docs (id, company, title, body, by, created, kind, file, bytes, pages, version, problems, updated) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			id, company, d.Title, u.Doc.Body, d.By, ts(now), d.Kind, d.File, d.Bytes, d.Pages, d.Version, string(problems), ts(now))
	} else {
		_, err = tx.Exec(`UPDATE docs SET title=?, body=?, by=?, kind=?, file=?, bytes=?, pages=?, version=?, problems=?, updated=? WHERE company=? AND id=?`,
			d.Title, u.Doc.Body, d.By, d.Kind, d.File, d.Bytes, d.Pages, d.Version, string(problems), ts(now), company, id)
	}
	if err != nil {
		os.Remove(stored)
		return Doc{}, "", 0, err
	}
	if err := putChunks(tx, id, parts); err != nil {
		return Doc{}, "", 0, err
	}
	var n int
	tx.QueryRow(`SELECT count(*) FROM chunks WHERE doc=?`, id).Scan(&n)
	d.Parts = n
	if err := addVersion(tx, d, filepath.Base(stored)); err != nil {
		return Doc{}, "", 0, err
	}
	if err := tx.Commit(); err != nil {
		os.Remove(stored)
		return Doc{}, "", 0, err
	}
	if outcome == Created {
		before = 0
	}
	d, err = s.Doc(company, id)
	return d, outcome, before, err
}

// keep writes a version's original file and says where.
func (s *Store) keep(company string, d Doc, data []byte) (string, error) {
	dir := filepath.Join(s.files, company, d.ID)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	ext := strings.ToLower(filepath.Ext(d.File))
	if len(ext) > 10 || strings.ContainsAny(ext, `/\\`) {
		ext = ""
	}
	p := filepath.Join(dir, fmt.Sprintf("v%d%s", d.Version, ext))
	return p, os.WriteFile(p, data, 0o640)
}

// Version is one upload of a document.
type Version struct {
	Version  int       `json:"version"`
	File     string    `json:"file,omitempty"`
	Kind     string    `json:"kind"`
	Bytes    int64     `json:"bytes"`
	Pages    int       `json:"pages,omitempty"`
	Parts    int       `json:"parts"`
	Problems []string  `json:"problems,omitempty"`
	By       string    `json:"by"`
	Created  time.Time `json:"created"`
	// Current is the version the knowledge base has; Download says whether its file is kept.
	Current  bool `json:"current"`
	Download bool `json:"download"`
}

// Versions are a document's uploads, newest first.
func (s *Store) Versions(company, id string) ([]Version, error) {
	d, err := s.Doc(company, id)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT version, file, stored, kind, bytes, pages, parts, problems, by, created FROM doc_versions WHERE doc=? ORDER BY version DESC`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Version{}
	for rows.Next() {
		var v Version
		var stored, problems, c string
		if err := rows.Scan(&v.Version, &v.File, &stored, &v.Kind, &v.Bytes, &v.Pages, &v.Parts, &problems, &v.By, &c); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(problems), &v.Problems)
		v.Created, v.Current = parseTS(c), v.Version == d.Version
		// a document typed in (no file) is downloaded as its text, the current version only
		v.Download = stored != "" || (v.Current && v.File == "")
		out = append(out, v)
	}
	return out, rows.Err()
}

// Original is a version's file to download: the path of the kept file, or (for a document typed
// in) its text.
type Original struct {
	Name string
	Path string
	Text string
}

// File is a version of a document to download (0: the current one).
func (s *Store) File(company, id string, version int) (Original, error) {
	d, body, err := s.DocBody(company, id)
	if err != nil {
		return Original{}, err
	}
	if version <= 0 {
		version = d.Version
	}
	var file, stored string
	err = s.db.QueryRow(`SELECT file, stored FROM doc_versions WHERE doc=? AND version=?`, id, version).Scan(&file, &stored)
	if errors.Is(err, sql.ErrNoRows) {
		return Original{}, ErrNotFound
	}
	if err != nil {
		return Original{}, err
	}
	if stored == "" {
		if version != d.Version || file != "" {
			return Original{}, ErrNotFound
		}
		return Original{Name: d.Title + ".md", Text: body}, nil
	}
	return Original{Name: file, Path: filepath.Join(s.files, company, id, stored)}, nil
}
