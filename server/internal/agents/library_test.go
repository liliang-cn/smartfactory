package agents

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"smartfactory/internal/extract"
)

// An uploaded file is kept with its passages; the same title again is its next version, the old
// file still there to download; a scan is kept with no passages; deleting removes the files.
func TestLibraryVersions(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.db.Close()
	v1 := Upload{File: "热锻工艺卡.md", Data: []byte("# 加热\n始锻 1200 ℃"), Doc: extract.Doc{Kind: "markdown", Body: "# 加热\n始锻 1200 ℃"}, By: "张工"}
	d, outcome, before, err := st.PutFile("huaduan", v1)
	if err != nil || outcome != Created || before != 0 || d.Title != "热锻工艺卡" || d.Version != 1 || d.Parts != 1 || d.File != "热锻工艺卡.md" {
		t.Fatalf("first upload: %+v %s %d %v", d, outcome, before, err)
	}
	v2 := Upload{File: "热锻工艺卡.md", Data: []byte("# 加热\n始锻 1210 ℃\n# 锻造\n三工位"), Doc: extract.Doc{Kind: "markdown", Body: "# 加热\n始锻 1210 ℃\n# 锻造\n三工位"}, By: "李工"}
	d2, outcome, before, err := st.PutFile("huaduan", v2)
	if err != nil || outcome != Updated || before != 1 || d2.ID != d.ID || d2.Version != 2 || d2.Parts != 2 || d2.By != "李工" {
		t.Fatalf("second upload: %+v %s %d %v", d2, outcome, before, err)
	}
	vs, err := st.Versions("huaduan", d.ID)
	if err != nil || len(vs) != 2 || !vs[0].Current || vs[1].Current || vs[1].Parts != 1 || !vs[1].Download {
		t.Fatalf("versions: %+v %v", vs, err)
	}
	old, err := st.File("huaduan", d.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(old.Path); string(b) != "# 加热\n始锻 1200 ℃" || old.Name != "热锻工艺卡.md" {
		t.Fatalf("version 1's file: %q %+v", b, old)
	}
	scan, _, _, err := st.PutFile("huaduan", Upload{File: "扫描.pdf", Data: []byte("%PDF"), Doc: extract.Doc{Kind: "pdf", Pages: 3, Problems: []string{"扫描件"}}, By: "张工"})
	if err != nil || scan.Parts != 0 || scan.Pages != 3 || len(scan.Problems) != 1 {
		t.Fatalf("a scan: %+v %v", scan, err)
	}
	if err := st.DeleteDoc("huaduan", d.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "files", "huaduan", d.ID)); !os.IsNotExist(err) {
		t.Errorf("the deleted document's files are still there: %v", err)
	}
	// a document typed in is downloaded as its text
	typed, err := st.AddDoc("huaduan", "点检表", "每班点检 IH-101", "张工")
	if err != nil {
		t.Fatal(err)
	}
	if o, err := st.File("huaduan", typed.ID, 0); err != nil || o.Text != "每班点检 IH-101" || o.Name != "点检表.md" {
		t.Fatalf("typed document: %+v %v", o, err)
	}
}

// A store made before files and versions opens with its documents as their first version.
func TestLibraryMigrates(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "agents.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE docs (id TEXT PRIMARY KEY, company TEXT NOT NULL, title TEXT NOT NULL, body TEXT NOT NULL, by TEXT NOT NULL, created TEXT NOT NULL);
		CREATE TABLE chunks (doc TEXT NOT NULL REFERENCES docs(id) ON DELETE CASCADE, seq INTEGER NOT NULL, heading TEXT NOT NULL, text TEXT NOT NULL, PRIMARY KEY (doc, seq));
		INSERT INTO docs VALUES ('d1', 'huaduan', '旧文档', '正文', '资料室', '2026-10-01T00:00:00Z');
		INSERT INTO chunks VALUES ('d1', 0, '', '正文');`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer st.db.Close()
	docs, err := st.Docs("huaduan")
	if err != nil || len(docs) != 1 || docs[0].Version != 1 || docs[0].Kind != "markdown" || docs[0].Parts != 1 {
		t.Fatalf("migrated: %+v %v", docs, err)
	}
	vs, err := st.Versions("huaduan", "d1")
	if err != nil || len(vs) != 1 || !vs[0].Current {
		t.Fatalf("migrated versions: %+v %v", vs, err)
	}
}
