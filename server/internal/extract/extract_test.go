package extract

import (
	"archive/zip"
	"bytes"
	"strconv"
	"strings"
	"testing"

	"github.com/go-pdf/fpdf"
	"github.com/xuri/excelize/v2"
	"golang.org/x/text/encoding/simplifiedchinese"
)

// DOCX builds a .docx with a heading 1, a "标题 2" heading, a paragraph and a table.
func DOCX(t testing.TB, heading, sub, para string, table [][]string) []byte {
	t.Helper()
	var doc strings.Builder
	doc.WriteString(`<?xml version="1.0" encoding="UTF-8"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`)
	p := func(style, text string) {
		doc.WriteString(`<w:p>`)
		if style != "" {
			doc.WriteString(`<w:pPr><w:pStyle w:val="` + style + `"/></w:pPr>`)
		}
		doc.WriteString(`<w:r><w:t xml:space="preserve">` + text + `</w:t></w:r></w:p>`)
	}
	p("Heading1", heading)
	p("a3", sub)
	p("", para)
	doc.WriteString(`<w:tbl>`)
	for _, r := range table {
		doc.WriteString(`<w:tr>`)
		for _, c := range r {
			doc.WriteString(`<w:tc><w:p><w:r><w:t>` + c + `</w:t></w:r></w:p></w:tc>`)
		}
		doc.WriteString(`</w:tr>`)
	}
	doc.WriteString(`</w:tbl></w:body></w:document>`)
	styles := `<?xml version="1.0" encoding="UTF-8"?><w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
<w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/></w:style>
<w:style w:type="paragraph" w:styleId="a3"><w:name w:val="标题 2"/></w:style></w:styles>`
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for name, body := range map[string]string{"word/document.xml": doc.String(), "word/styles.xml": styles, "[Content_Types].xml": `<Types/>`} {
		w, _ := z.Create(name)
		w.Write([]byte(body))
	}
	z.Close()
	return buf.Bytes()
}

// XLSX builds a workbook with one sheet of rows.
func XLSX(t testing.TB, sheet string, table [][]string) []byte {
	t.Helper()
	f := excelize.NewFile()
	f.SetSheetName("Sheet1", sheet)
	for i, r := range table {
		row := make([]any, len(r))
		for j, c := range r {
			row[j] = c
		}
		cell, _ := excelize.CoordinatesToCellName(1, i+1)
		f.SetSheetRow(sheet, cell, &row)
	}
	b, err := f.WriteToBuffer()
	if err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// PDF builds a PDF of pages of Latin text; an empty string is a page with only a drawing.
func PDF(t testing.TB, pages ...string) []byte {
	t.Helper()
	p := fpdf.New("P", "mm", "A4", "")
	p.SetFont("Helvetica", "", 12)
	for _, text := range pages {
		p.AddPage()
		if text == "" {
			p.Rect(20, 20, 100, 60, "F")
			continue
		}
		for _, line := range strings.Split(text, "\n") {
			p.Cell(0, 8, line)
			p.Ln(8)
		}
	}
	var buf bytes.Buffer
	if err := p.Output(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestDOCX(t *testing.T) {
	d, err := Extract("热锻工艺卡.docx", DOCX(t, "曲轴热锻工艺卡", "1 加热", "始锻温度 1180～1230 ℃，IH-101 出炉后 8 秒内上锤。",
		[][]string{{"参数", "目标", "上限"}, {"始锻温度", "1200 ℃", "1230 ℃"}, {"终锻温度", "950 ℃", ""}}))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# 曲轴热锻工艺卡", "## 1 加热", "IH-101 出炉后", "参数=始锻温度；目标=1200 ℃；上限=1230 ℃", "参数=终锻温度；目标=950 ℃"} {
		if !strings.Contains(d.Body, want) {
			t.Errorf("docx body has no %q:\n%s", want, d.Body)
		}
	}
}

func TestXLSX(t *testing.T) {
	d, err := Extract("参数表.xlsx", XLSX(t, "加热参数", [][]string{{"设备", "参数", "值"}, {"IH-101", "功率", "800 kW"}, {"IH-201", "功率", "650 kW"}}))
	if err != nil {
		t.Fatal(err)
	}
	if d.Kind != "xlsx" || !strings.Contains(d.Body, "# 加热参数") || !strings.Contains(d.Body, "设备=IH-101；参数=功率；值=800 kW") {
		t.Fatalf("xlsx:\n%s", d.Body)
	}
}

func TestPDF(t *testing.T) {
	d, err := Extract("manual.pdf", PDF(t, "Press HP-101 die change\nTorque 450 Nm", "Inspect the die every 5000 hits"))
	if err != nil {
		t.Fatal(err)
	}
	if d.Pages != 2 || !strings.Contains(d.Body, "# 第 1 页") || !strings.Contains(d.Body, "# 第 2 页") ||
		!strings.Contains(d.Body, "HP-101") || !strings.Contains(d.Body, "5000 hits") {
		t.Fatalf("pdf: %d pages\n%s", d.Pages, d.Body)
	}
	scan, err := Extract("scan.pdf", PDF(t, "", ""))
	if err != nil {
		t.Fatal(err)
	}
	if scan.Body != "" || len(scan.Problems) == 0 || !strings.Contains(scan.Problems[0], "扫描件") {
		t.Fatalf("a scan: %+v", scan)
	}
}

// Headings typed by hand (bold and large, or bold and numbered) are headings; a bold sentence is not.
func TestHandFormattedHeadings(t *testing.T) {
	run := func(text string, bold bool, size int) string {
		rpr := ""
		if bold {
			rpr += `<w:b/>`
		}
		if size > 0 {
			rpr += `<w:sz w:val="` + strconv.Itoa(size) + `"/>`
		}
		return `<w:p><w:r><w:rPr>` + rpr + `</w:rPr><w:t>` + text + `</w:t></w:r></w:p>`
	}
	body := `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>` +
		run("热处理作业指导书", true, 44) + run("3.2 回火", true, 24) + run("注意：回火炉门必须关闭后才能升温。", true, 24) + run("正文", false, 24) +
		`</w:body></w:document>`
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	w, _ := z.Create("word/document.xml")
	w.Write([]byte(body))
	z.Close()
	d, err := Extract("热处理.docx", buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d.Body, "# 热处理作业指导书") || !strings.Contains(d.Body, "### 3.2 回火") || strings.Contains(d.Body, "# 注意") {
		t.Fatalf("hand-formatted headings:\n%s", d.Body)
	}
}

func TestRadicals(t *testing.T) {
	if got := radicals("热模锻压⼒机三⼯位，1180～1230 ℃"); got != "热模锻压力机三工位，1180～1230 ℃" {
		t.Errorf("radicals: %s", got)
	}
}

func TestText(t *testing.T) {
	gbk, _ := simplifiedchinese.GBK.NewEncoder().Bytes([]byte("中频感应加热炉 IH-101 每班点检"))
	d, err := Extract("点检.txt", gbk)
	if err != nil {
		t.Fatal(err)
	}
	if d.Body != "中频感应加热炉 IH-101 每班点检" || len(d.Problems) != 1 || !strings.Contains(d.Problems[0], "GBK") {
		t.Fatalf("gbk: %+v", d)
	}
	d, _ = Extract("说明.md", append([]byte{0xEF, 0xBB, 0xBF}, []byte("# 标题")...))
	if d.Body != "# 标题" || len(d.Problems) != 0 {
		t.Fatalf("utf-8 with a BOM: %+v", d)
	}
	csvGBK, _ := simplifiedchinese.GBK.NewEncoder().Bytes([]byte("设备,参数,值\nHP-101,吨位,2500 t\n"))
	d, _ = Extract("设备.csv", csvGBK)
	if !strings.Contains(d.Body, "设备=HP-101；参数=吨位；值=2500 t") {
		t.Fatalf("csv: %+v", d)
	}
	if _, err := Extract("旧.doc", []byte("x")); err == nil || !strings.Contains(err.Error(), ".docx") {
		t.Errorf("an old .doc: %v", err)
	}
}
