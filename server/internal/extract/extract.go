// Package extract reads the documents a factory keeps — PDF work instructions, Word process cards,
// Excel parameter sheets, text in UTF-8 or GBK — into Markdown the library cuts into passages:
// headings stay headings, a PDF's pages become "第 N 页" sections so a passage can cite its page,
// and every table row becomes a line of "header=value" pairs so a parameter is found by its name.
// Pure Go: no converters to install.
package extract

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/unicode/norm"
)

// Doc is what a file says, ready for the library.
type Doc struct {
	// Kind: pdf | docx | xlsx | csv | markdown | text
	Kind string
	// Body is Markdown; headings (#…####) are where passages are cut.
	Body  string
	Pages int
	// Problems are what could not be read, said so a person can act on it.
	Problems []string
}

// ErrUnsupported is a file type the library does not read.
var ErrUnsupported = errors.New("unsupported")

// MaxRows is how many table rows a workbook or CSV contributes before the rest are left out.
const MaxRows = 20000

// Kinds are the extensions the library reads, for the upload pickers.
var Kinds = []string{".pdf", ".docx", ".xlsx", ".xlsm", ".csv", ".md", ".markdown", ".txt"}

// Extract reads data by its file name's extension.
func Extract(name string, data []byte) (d Doc, err error) {
	defer func() {
		// a malformed file must not take the server down
		if r := recover(); r != nil {
			err = fmt.Errorf("文件读不出来（可能已损坏）：%v", r)
		}
		d.Body = radicals(d.Body)
	}()
	return extract(name, data)
}

// radicals writes the Kangxi radicals some PDFs encode their characters as (⼯ for 工, ⼒ for
// 力) as the characters themselves, so a search for 工艺 finds them; other text is left as it is.
func radicals(s string) string {
	if !strings.ContainsFunc(s, isRadical) {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if isRadical(r) {
			b.WriteString(norm.NFKC.String(string(r)))
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isRadical(r rune) bool { return r >= 0x2E80 && r <= 0x2FDF }

func extract(name string, data []byte) (Doc, error) {
	switch ext := strings.ToLower(filepath.Ext(name)); ext {
	case ".pdf":
		return readPDF(data)
	case ".docx":
		return readDOCX(data)
	case ".xlsx", ".xlsm":
		return readXLSX(data)
	case ".csv":
		text, enc := Decode(data)
		d, err := readCSV(text)
		if enc != "" {
			d.Problems = append(d.Problems, "按 "+enc+" 编码读取")
		}
		return d, err
	case ".md", ".markdown", ".txt", "":
		text, enc := Decode(data)
		kind := "markdown"
		if ext == ".txt" || ext == "" {
			kind = "text"
		}
		d := Doc{Kind: kind, Body: text}
		if enc != "" {
			d.Problems = append(d.Problems, "按 "+enc+" 编码读取")
		}
		if strings.TrimSpace(text) == "" {
			d.Problems = append(d.Problems, "文件是空的")
		}
		return d, nil
	case ".doc", ".xls", ".ppt":
		return Doc{}, fmt.Errorf("%w：%s 是旧版 Office 格式，另存为 %sx 再上传", ErrUnsupported, ext, ext)
	default:
		return Doc{}, fmt.Errorf("%w：读不了 %s 文件；可以上传 PDF、Word（.docx）、Excel（.xlsx）、CSV、Markdown 或文本", ErrUnsupported, ext)
	}
}

// Decode reads text in UTF-8 (with or without a BOM), UTF-16 with a BOM, or GBK / GB18030, and
// says which encoding it took when it was not UTF-8.
func Decode(b []byte) (text, encoding string) {
	switch {
	case bytes.HasPrefix(b, []byte{0xEF, 0xBB, 0xBF}):
		return string(b[3:]), ""
	case bytes.HasPrefix(b, []byte{0xFF, 0xFE}), bytes.HasPrefix(b, []byte{0xFE, 0xFF}):
		le := b[0] == 0xFF
		b = b[2:]
		u := make([]uint16, len(b)/2)
		for i := range u {
			if le {
				u[i] = uint16(b[2*i]) | uint16(b[2*i+1])<<8
			} else {
				u[i] = uint16(b[2*i])<<8 | uint16(b[2*i+1])
			}
		}
		return string(utf16.Decode(u)), "UTF-16"
	case utf8.Valid(b):
		return string(b), ""
	}
	out, err := simplifiedchinese.GB18030.NewDecoder().Bytes(b)
	if err != nil {
		return strings.ToValidUTF8(string(b), "�"), "未知（按 UTF-8 尽量读取）"
	}
	return string(out), "GBK"
}

// rows writes a table as passages: each row one line of "header=value" pairs (cells without a
// header by their column number), the first row with two or more cells taken as the header. A
// table of a single row is its cells.
func rows(table [][]string, limit int) (string, int) {
	var b strings.Builder
	var head []string
	n := 0
	for _, r := range table {
		cells := trimRow(r)
		if len(cells) == 0 {
			continue
		}
		if head == nil && filled(cells) >= 2 {
			head = cells
			continue
		}
		if n >= limit {
			break
		}
		var parts []string
		for i, c := range cells {
			if c == "" {
				continue
			}
			h := ""
			if i < len(head) {
				h = head[i]
			}
			if h == "" || h == c {
				if head == nil {
					parts = append(parts, c)
				} else {
					parts = append(parts, fmt.Sprintf("第%d列=%s", i+1, c))
				}
				continue
			}
			parts = append(parts, h+"="+c)
		}
		if len(parts) > 0 {
			b.WriteString(strings.Join(parts, "；"))
			b.WriteByte('\n')
			n++
		}
	}
	if head != nil && n == 0 {
		// only a header: still say what the table has
		b.WriteString(strings.Join(head, "；") + "\n")
	}
	return b.String(), n
}

func trimRow(r []string) []string {
	out := make([]string, len(r))
	for i, c := range r {
		out[i] = strings.Join(strings.Fields(c), " ")
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

func filled(r []string) int {
	n := 0
	for _, c := range r {
		if c != "" {
			n++
		}
	}
	return n
}

func readCSV(text string) (Doc, error) {
	r := csv.NewReader(strings.NewReader(text))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	table, err := r.ReadAll()
	if err != nil && len(table) == 0 {
		return Doc{}, fmt.Errorf("CSV 读不出来：%w", err)
	}
	body, n := rows(table, MaxRows)
	d := Doc{Kind: "csv", Body: body}
	if n >= MaxRows {
		d.Problems = append(d.Problems, fmt.Sprintf("只取了前 %d 行", MaxRows))
	}
	if strings.TrimSpace(body) == "" {
		d.Problems = append(d.Problems, "表格是空的")
	}
	return d, nil
}
