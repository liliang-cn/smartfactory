package web

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"golang.org/x/text/encoding/simplifiedchinese"

	"smartfactory/internal/agents"
	"smartfactory/internal/audit"
	"smartfactory/internal/auth"
	"smartfactory/internal/engine"
	"smartfactory/internal/extract/extracttest"
	"smartfactory/internal/knowledge"
	"smartfactory/internal/sfptest"
	"smartfactory/internal/tags"
)

// A factory's own documents go into the library as files — Word, Excel, PDF, GBK text — and
// their passages into the knowledge base, linked to the machines they name; a file uploaded
// again is the next version, the old one still there to download; a scan and an old .doc are
// answered with what to do about them.
func TestLibraryUpload(t *testing.T) {
	if _, err := os.Stat("../engine/js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	t.Setenv("LLM_API_KEY", "")
	t.Setenv("AGENTGO_HOME", t.TempDir())
	std := sfptest.Load(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng, err := engine.New(log)
	if err != nil {
		t.Fatal(err)
	}
	eng.Dir = t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); <-eng.Done })
	go eng.Run(ctx)
	ag, err := agents.New(t.TempDir(), eng, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ag.Close() })
	kb, err := knowledge.Open(t.TempDir(), eng, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(kb.Close)
	ag.KB = kb
	if err := kb.SyncProcess(ctx, "huaduan"); err != nil {
		t.Fatal(err)
	}
	trail, _ := audit.Open(t.TempDir(), log)
	t.Cleanup(func() { trail.Close() })
	ag.Audit = trail

	admin, ak := auth.NewToken("厂长", auth.Admin, []string{"huaduan"}, "")
	member, mk := auth.NewToken("计划员", auth.Member, []string{"huaduan"}, "")
	ts := httptest.NewServer((&Server{Hub: tags.NewHub(), Engine: eng, Keys: keyring(t, "", ak, mk), Audit: trail, Log: log, Agents: ag, Knowledge: kb}).Handler())
	t.Cleanup(ts.Close)

	do := func(token, method, path string, body io.Reader, ctype string) (int, []byte) {
		t.Helper()
		req, _ := http.NewRequest(method, ts.URL+path, body)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		if ctype != "" {
			req.Header.Set("Content-Type", ctype)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, b
	}
	upload := func(token string, files map[string][]byte, order ...string) (int, []UploadResult, []byte) {
		t.Helper()
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		for _, name := range order {
			w, _ := mw.CreateFormFile("file", name)
			w.Write(files[name])
		}
		mw.Close()
		code, b := do(token, "POST", "/api/library/huaduan/files", &buf, mw.FormDataContentType())
		var res []UploadResult
		json.Unmarshal(b, &res)
		return code, res, b
	}

	card := extracttest.DOCX(t, "曲轴热锻工艺卡", "1 加热", "ih101 出炉后 8 秒内上锤，送 HP 系列压力机。",
		[][]string{{"参数", "目标", "上限"}, {"始锻温度", "1200 ℃", "1230 ℃"}})
	gbk, _ := simplifiedchinese.GBK.NewEncoder().Bytes([]byte("中频感应加热炉每班点检：感应圈水温、绝缘。"))
	files := map[string][]byte{
		"热锻工艺卡.docx":     card,
		"压力机参数.xlsx":     extracttest.XLSX(t, "打击力", [][]string{{"设备", "参数", "值"}, {"HP-101", "打击力", "25000 kN"}}),
		"die-manual.pdf": extracttest.PDF(t, "Press HP-101 die change\nTorque 450 Nm", "Inspect every 5000 hits"),
		"点检.txt":         gbk,
		"扫描件.pdf":        extracttest.PDF(t, "", ""),
		"旧版.doc":         []byte("not a docx"),
	}
	if code, _, _ := upload(member, files, "点检.txt"); code != 403 {
		t.Errorf("a member uploaded a document: %d", code)
	}
	code, res, b := upload(admin, files, "热锻工艺卡.docx", "压力机参数.xlsx", "die-manual.pdf", "点检.txt", "扫描件.pdf", "旧版.doc")
	if code != 200 || len(res) != 6 {
		t.Fatalf("upload: %d %s", code, b)
	}
	std.Check("UploadResults", b)
	by := map[string]UploadResult{}
	for _, r := range res {
		by[r.File] = r
	}
	if r := by["热锻工艺卡.docx"]; !r.OK || r.Outcome != agents.Created || r.Doc.Kind != "docx" || r.Doc.Title != "热锻工艺卡" || r.Doc.Parts == 0 {
		t.Errorf("docx: %+v", r)
	}
	if r := by["die-manual.pdf"]; !r.OK || r.Doc.Pages != 2 || r.Doc.Parts == 0 {
		t.Errorf("pdf: %+v", r)
	}
	if r := by["点检.txt"]; !r.OK || r.Doc.Kind != "text" || len(r.Doc.Problems) != 1 || !strings.Contains(r.Doc.Problems[0], "GBK") {
		t.Errorf("gbk text: %+v", r)
	}
	if r := by["扫描件.pdf"]; !r.OK || r.Doc.Parts != 0 || len(r.Doc.Problems) == 0 || !strings.Contains(r.Doc.Problems[0], "扫描件") {
		t.Errorf("scan: %+v", r)
	}
	if r := by["旧版.doc"]; r.OK || !strings.Contains(r.Error, ".docx") {
		t.Errorf("an old .doc: %+v", r)
	}

	// the same file again: its next version
	card2 := extracttest.DOCX(t, "曲轴热锻工艺卡", "1 加热", "ih101 出炉后 6 秒内上锤。", [][]string{{"参数", "目标"}, {"始锻温度", "1210 ℃"}})
	files["热锻工艺卡.docx"] = card2
	_, res, _ = upload(admin, files, "热锻工艺卡.docx")
	id := by["热锻工艺卡.docx"].Doc.ID
	if len(res) != 1 || res[0].Outcome != agents.Updated || res[0].Doc.ID != id || res[0].Doc.Version != 2 {
		t.Fatalf("second upload: %+v", res)
	}
	_, b = do("", "GET", "/api/library/huaduan/docs", nil, "")
	std.Check("Docs", b)
	_, b = do("", "GET", "/api/library/huaduan/docs/"+id+"/versions", nil, "")
	std.Check("DocVersions", b)
	var vs []agents.Version
	json.Unmarshal(b, &vs)
	if len(vs) != 2 || !vs[0].Current || vs[1].Current {
		t.Fatalf("versions: %s", b)
	}
	if code, got := do(member, "GET", "/api/library/huaduan/docs/"+id+"/file?version=1", nil, ""); code != 200 || !bytes.Equal(got, card) {
		t.Errorf("version 1's file: %d, %d bytes", code, len(got))
	}
	if code, _ := do("", "GET", "/api/library/huaduan/docs/"+id+"/file", nil, ""); code != 403 {
		t.Errorf("a visitor downloaded a document: %d", code)
	}

	// the knowledge base has the current version, linked to what it names
	a, err := kb.Search(ctx, "huaduan", "IH-101 始锻温度", []string{"IH-101"}, nil, 6)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(a.Passages, func(p knowledge.Passage) bool { return p.Doc == id && strings.Contains(p.Text, "1210") }) {
		t.Errorf("the current card is not found: %+v", a.Passages)
	}
	if slices.ContainsFunc(a.Passages, func(p knowledge.Passage) bool { return p.Doc == id && strings.Contains(p.Text, "1230") }) {
		t.Errorf("the replaced version is still in the knowledge base: %+v", a.Passages)
	}
	e, err := kb.Lookup(ctx, "huaduan", "IH-101")
	if err != nil || e == nil || !slices.ContainsFunc(e.Mentions, func(m string) bool { return strings.Contains(m, "热锻工艺卡") }) {
		t.Errorf("IH-101 does not know the card names it (as ih101): %+v %v", e, err)
	}
	a, _ = kb.Search(ctx, "huaduan", "HP-101 打击力", []string{"HP-101"}, nil, 6)
	if !slices.ContainsFunc(a.Passages, func(p knowledge.Passage) bool { return strings.Contains(p.Text, "25000 kN") }) {
		t.Errorf("the parameter sheet's row is not found: %+v", a.Passages)
	}

	entries, _ := trail.List(t.Context(), "huaduan", 0, 50)
	var actions []string
	for _, e := range entries {
		actions = append(actions, e.Action)
	}
	if !slices.Contains(actions, "doc.add") || !slices.Contains(actions, "doc.update") {
		t.Errorf("activity: %v", actions)
	}
	if code, _ := do(admin, "DELETE", "/api/library/huaduan/docs/"+id, nil, ""); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	if code, _ := do("", "GET", "/api/library/huaduan/docs/"+id+"/versions", nil, ""); code != 404 {
		t.Errorf("a deleted document's versions: %d", code)
	}
}
