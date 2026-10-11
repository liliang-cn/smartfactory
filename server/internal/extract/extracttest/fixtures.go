// Package extracttest builds small Word, Excel and PDF files for tests.
package extracttest

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"

	"github.com/go-pdf/fpdf"
	"github.com/xuri/excelize/v2"
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
