package extract

import (
	"bytes"
	"fmt"
	"strings"
	"unicode"

	"github.com/ledongthuc/pdf"
)

// readPDF writes each page as a "第 N 页" section, its text in reading order, one line per row.
// A PDF with no text on any page is a scan: it is kept, and said to have no text.
func readPDF(data []byte) (Doc, error) {
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "encrypt") {
			return Doc{}, fmt.Errorf("PDF 加了密码，去掉密码再上传")
		}
		return Doc{}, fmt.Errorf("PDF 读不出来：%w", err)
	}
	d := Doc{Kind: "pdf", Pages: r.NumPage()}
	var b strings.Builder
	chars, empty := 0, 0
	for i := 1; i <= d.Pages; i++ {
		text := pageText(r.Page(i))
		if strings.TrimSpace(text) == "" {
			empty++
			continue
		}
		chars += len([]rune(text))
		fmt.Fprintf(&b, "# 第 %d 页\n\n%s\n\n", i, text)
	}
	d.Body = b.String()
	switch {
	case chars < 20:
		d.Body = ""
		d.Problems = append(d.Problems, "扫描件，没有可以读取的文字（暂不识别图片里的字）")
	case empty > 0:
		d.Problems = append(d.Problems, fmt.Sprintf("%d 页没有文字（可能是图片）", empty))
	}
	return d, nil
}

func pageText(p pdf.Page) (text string) {
	defer func() {
		if recover() != nil {
			text = ""
		}
	}()
	if p.V.IsNull() {
		return ""
	}
	rows, err := p.GetTextByRow()
	if err != nil || len(rows) == 0 {
		t, _ := p.GetPlainText(nil)
		return strings.TrimSpace(t)
	}
	var b strings.Builder
	for _, row := range rows {
		var line strings.Builder
		for _, t := range row.Content {
			s := t.S
			if s == "" {
				continue
			}
			// two runs of Latin letters or digits on one row are two words
			if line.Len() > 0 && wordy(lastRune(line.String())) && wordy(firstRune(s)) {
				line.WriteByte(' ')
			}
			line.WriteString(s)
		}
		if l := strings.TrimSpace(line.String()); l != "" {
			b.WriteString(l)
			b.WriteByte('\n')
		}
	}
	return strings.TrimSpace(b.String())
}

func wordy(r rune) bool { return r < 0x2E80 && (unicode.IsLetter(r) || unicode.IsDigit(r)) }

func lastRune(s string) rune {
	r := []rune(s)
	if len(r) == 0 {
		return 0
	}
	return r[len(r)-1]
}

func firstRune(s string) rune {
	for _, r := range s {
		return r
	}
	return 0
}
