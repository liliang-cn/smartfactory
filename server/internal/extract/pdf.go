package extract

import (
	"bytes"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/ledongthuc/pdf"
	"golang.org/x/text/unicode/norm"
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

// pageText lays a page's text out by position: runs of glyphs grouped into rows from the top
// of the page down, each row read left to right. It reads the positions after the page's
// transforms (Chrome and other Skia writers draw with the y axis flipped, so the library's own
// row reader puts their pages bottom first). Glyph widths are not known, so a run's end is
// estimated from its characters: a run that starts where the last one ends continues it (a
// number kerned apart, "1" and "180", is one number), a gap the size of a space is a space, and
// a gap wider than most of a character is the next column of a table, written " | ".
func pageText(p pdf.Page) (text string) {
	defer func() {
		if recover() != nil {
			text = ""
		}
	}()
	if p.V.IsNull() {
		return ""
	}
	type run struct {
		x, y, fs float64
		s        strings.Builder
	}
	var runs []*run
	for _, g := range p.Content().Text {
		if g.S == "" {
			continue
		}
		if n := len(runs); n > 0 && runs[n-1].x == g.X && runs[n-1].y == g.Y {
			runs[n-1].s.WriteString(g.S)
			continue
		}
		r := &run{x: g.X, y: g.Y, fs: g.FontSize}
		if r.fs <= 0 {
			r.fs = 10
		}
		r.s.WriteString(g.S)
		runs = append(runs, r)
	}
	if len(runs) == 0 {
		t, _ := p.GetPlainText(nil)
		return strings.TrimSpace(fold(t))
	}
	// rows, top first: a run within half a character of a row's baseline is on that row
	sort.SliceStable(runs, func(i, j int) bool { return runs[i].y > runs[j].y })
	var rows [][]*run
	for _, r := range runs {
		if n := len(rows); n > 0 && math.Abs(rows[n-1][0].y-r.y) < 0.5*r.fs {
			rows[n-1] = append(rows[n-1], r)
			continue
		}
		rows = append(rows, []*run{r})
	}
	var b strings.Builder
	for _, row := range rows {
		sort.SliceStable(row, func(i, j int) bool { return row[i].x < row[j].x })
		var line strings.Builder
		end := 0.0
		for _, r := range row {
			s := r.s.String()
			if line.Len() > 0 {
				gap := r.x - end
				switch {
				case gap > 0.7*r.fs:
					line.WriteString(" | ")
				case gap > 0.2*r.fs && lastRune(line.String()) != ' ' && firstRune(s) != ' ':
					line.WriteByte(' ')
				}
			}
			line.WriteString(s)
			end = r.x + width(s)*r.fs
		}
		if l := strings.Join(strings.Fields(line.String()), " "); l != "" {
			b.WriteString(l)
			b.WriteByte('\n')
		}
	}
	return strings.TrimSpace(fold(b.String()))
}

// width estimates how many ems a run takes: a Chinese character or full-width sign one, a
// space a quarter, other characters a little over half.
func width(s string) (w float64) {
	for _, r := range s {
		switch {
		case r == ' ':
			w += 0.25
		case r >= 0x2E80 || (r >= 0xFF00 && r <= 0xFFEF):
			w += 1
		default:
			w += 0.55
		}
	}
	return w
}

// fold writes Kangxi radicals as the characters they look like. Some PDF writers (Chrome among
// them) map 用, 水, 高 … to the radical code points (⽤ U+2F64), which read the same but never
// match a search for the real character.
func fold(s string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 0x2F00 && r <= 0x2FDF) || (r >= 0x2E80 && r <= 0x2EFF) {
			if n := []rune(norm.NFKC.String(string(r))); len(n) == 1 {
				return n[0]
			}
		}
		return r
	}, s)
}

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
