package extract

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// readDOCX walks word/document.xml in order: paragraphs styled as headings (by the style's name
// or outline level, "Heading 2" and "标题 2" alike) become Markdown headings, the rest text, and
// each table its rows of "header=value".
func readDOCX(data []byte) (Doc, error) {
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return Doc{}, fmt.Errorf("不是有效的 .docx 文件：%w", err)
	}
	files := map[string]*zip.File{}
	for _, f := range z.File {
		files[f.Name] = f
	}
	doc := files["word/document.xml"]
	if doc == nil {
		return Doc{}, fmt.Errorf("不是有效的 .docx 文件：没有 word/document.xml")
	}
	levels := map[string]int{}
	if s := files["word/styles.xml"]; s != nil {
		levels = headingStyles(s)
	}
	rc, err := doc.Open()
	if err != nil {
		return Doc{}, err
	}
	defer rc.Close()
	body, err := walkDocument(rc, levels)
	if err != nil {
		return Doc{}, fmt.Errorf(".docx 读不出来：%w", err)
	}
	d := Doc{Kind: "docx", Body: body}
	if strings.TrimSpace(body) == "" {
		d.Problems = append(d.Problems, "文档里没有文字（可能只有图片）")
	}
	return d, nil
}

var headingName = regexp.MustCompile(`(?i)^(heading|标题)\s*([1-9])$`)

// headingStyles maps each paragraph style to its heading level (1–4).
func headingStyles(f *zip.File) map[string]int {
	out := map[string]int{}
	rc, err := f.Open()
	if err != nil {
		return out
	}
	defer rc.Close()
	var styles struct {
		Style []struct {
			ID   string `xml:"styleId,attr"`
			Name struct {
				Val string `xml:"val,attr"`
			} `xml:"name"`
			PPr struct {
				Outline *struct {
					Val string `xml:"val,attr"`
				} `xml:"outlineLvl"`
			} `xml:"pPr"`
		} `xml:"style"`
	}
	if xml.NewDecoder(rc).Decode(&styles) != nil {
		return out
	}
	for _, s := range styles.Style {
		if m := headingName.FindStringSubmatch(strings.TrimSpace(s.Name.Val)); m != nil {
			out[s.ID], _ = strconv.Atoi(m[2])
		} else if s.PPr.Outline != nil {
			if n, err := strconv.Atoi(s.PPr.Outline.Val); err == nil && n < 9 {
				out[s.ID] = n + 1
			}
		}
	}
	return out
}

func walkDocument(r io.Reader, levels map[string]int) (string, error) {
	dec := xml.NewDecoder(r)
	var out strings.Builder
	var (
		para    strings.Builder // the paragraph being read
		style   string
		outline = -1
		depth   int // tables open
		table   [][]string
		row     []string
		cell    strings.Builder
		inText  bool
		// how the paragraph's runs look, for documents formatted by hand rather than with styles
		inRun, runBold, allBold bool
		runSize, size           int
		runs                    int
	)
	endPara := func() {
		text := strings.TrimSpace(para.String())
		para.Reset()
		level := levels[style]
		if outline >= 0 && outline < 9 {
			level = outline + 1
		}
		if level == 0 && runs > 0 && allBold {
			level = looksLikeHeading(text, size)
		}
		style, outline = "", -1
		runs, allBold, size = 0, true, 0
		if text == "" {
			return
		}
		if depth > 0 {
			if cell.Len() > 0 {
				cell.WriteByte(' ')
			}
			cell.WriteString(text)
			return
		}
		if level > 0 {
			out.WriteString("\n" + strings.Repeat("#", min(level, 4)) + " " + text + "\n\n")
			return
		}
		out.WriteString(text + "\n\n")
	}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "tbl":
				depth++
				if depth == 1 {
					table = nil
				}
			case "tr":
				if depth == 1 {
					row = nil
				}
			case "tc":
				if depth == 1 {
					cell.Reset()
				}
			case "p":
				runs, allBold, size = 0, true, 0
			case "r":
				inRun, runBold, runSize = true, false, 0
			case "b":
				if inRun {
					v := attr(t, "val")
					runBold = v == "" || v == "1" || v == "true" || v == "on"
				}
			case "sz":
				if inRun {
					runSize, _ = strconv.Atoi(attr(t, "val"))
				}
			case "pStyle":
				style = attr(t, "val")
			case "outlineLvl":
				if n, err := strconv.Atoi(attr(t, "val")); err == nil {
					outline = n
				}
			case "t":
				inText = true
			case "tab":
				para.WriteByte('\t')
			case "br", "cr":
				para.WriteByte('\n')
			}
		case xml.CharData:
			if inText {
				para.Write(t)
				if strings.TrimSpace(string(t)) != "" {
					runs++
					allBold = allBold && runBold
					size = max(size, runSize)
				}
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "t":
				inText = false
			case "r":
				inRun = false
			case "p":
				endPara()
			case "tc":
				if depth == 1 {
					row = append(row, strings.TrimSpace(cell.String()))
					cell.Reset()
				}
			case "tr":
				if depth == 1 {
					table = append(table, row)
				}
			case "tbl":
				depth--
				if depth == 0 {
					text, _ := rows(table, MaxRows)
					if text != "" {
						out.WriteString(text + "\n")
					}
				}
			}
		}
	}
	return strings.TrimSpace(out.String()) + "\n", nil
}

var numbered = regexp.MustCompile(`^(\d+(\.\d+)*[\s、.]|[一二三四五六七八九十]+、|第[一二三四五六七八九十\d]+[章节条部分])`)

// looksLikeHeading is the level of a short, all-bold paragraph that reads as a heading: set large
// (size is in half-points) or numbered like one. 0 when it is emphasis, not a heading.
func looksLikeHeading(text string, size int) int {
	n := len([]rune(text))
	if n > 40 || strings.HasSuffix(text, "。") || strings.HasSuffix(text, "：") {
		return 0
	}
	switch {
	case size >= 40:
		return 1
	case size >= 32:
		return 2
	case size >= 28 || numbered.MatchString(text):
		return 3
	}
	return 0
}

func attr(e xml.StartElement, local string) string {
	for _, a := range e.Attr {
		if a.Name.Local == local {
			return a.Value
		}
	}
	return ""
}
