package stores

import (
	"fmt"
	"strconv"
	"strings"
)

// Reading a BOM from a spreadsheet the way factories keep them: a header row (物料编码, 名称, 规格,
// 用量, 单位 …, in whatever order and wording), then one row per component, its place in the tree
// given by a level column (1, 2 … or .1, ..2, or outline numbers 1.2.1) or by a parent-code
// column, or flat when there is neither.

var columns = map[string][]string{
	"level":  {"层级", "层次", "级别", "阶层", "阶次", "bom层级", "level", "lvl"},
	"parent": {"上级编码", "上级物料", "上级料号", "上级", "父项编码", "父件编码", "父项", "父件", "parent"},
	"code":   {"物料编码", "子件编码", "子项编码", "物料号", "编码", "料号", "零件号", "图号", "代号", "code", "item", "partno", "part"},
	"name":   {"物料名称", "子件名称", "子项名称", "名称", "品名", "name", "description"},
	"kind":   {"物料类型", "物料属性", "类型", "类别", "属性", "来源", "kind", "type"},
	"spec":   {"规格型号", "规格", "型号", "尺寸", "spec", "size"},
	"grade":  {"牌号", "材质", "材料", "钢号", "grade", "material"},
	"qty":    {"单件用量", "单位用量", "用量", "数量", "定额", "qty", "quantity"},
	"unit":   {"计量单位", "单位", "unit", "uom"},
	"scrap":  {"损耗率", "损耗", "废品率", "scrap"},
	"op":     {"使用工序", "工序", "工位", "op", "operation"},
	"note":   {"备注", "说明", "note", "remark"},
}

// order the columns are matched in: a header of 上级编码 is the parent, not the code.
var columnOrder = []string{"level", "parent", "code", "name", "kind", "spec", "grade", "qty", "unit", "scrap", "op", "note"}

func columnOf(h string) string {
	h = strings.ToLower(strings.Join(strings.Fields(h), ""))
	h = strings.NewReplacer("（", "(", "）", ")", "(%)", "", "%", "", "*", "").Replace(h)
	for _, c := range columnOrder {
		for _, a := range columns[c] {
			if h == a {
				return c
			}
		}
	}
	for _, c := range columnOrder {
		for _, a := range columns[c] {
			if len([]rune(a)) >= 2 && strings.HasPrefix(h, a) {
				return c
			}
		}
	}
	return ""
}

// kindOf is the kind of line a cell says, or "" when it says none the import knows.
func kindOf(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch {
	case s == "":
		return ""
	case strings.Contains(s, "标准"), strings.Contains(s, "紧固"), s == "std":
		return Std
	case strings.Contains(s, "原材料"), strings.Contains(s, "原料"), strings.Contains(s, "棒料"), strings.Contains(s, "钢材"), strings.Contains(s, "坯料"), s == "raw":
		return Raw
	case strings.Contains(s, "辅料"), strings.Contains(s, "耗材"), strings.Contains(s, "消耗"), strings.Contains(s, "包装"), s == "aux":
		return Aux
	case strings.Contains(s, "自制"), strings.Contains(s, "半成品"), strings.Contains(s, "组件"), strings.Contains(s, "总成"), strings.Contains(s, "锻件"), s == "make":
		return Make
	case strings.Contains(s, "外购"), strings.Contains(s, "采购"), strings.Contains(s, "外协"), s == "buy":
		return Buy
	}
	return ""
}

func number(s string) (float64, bool) {
	s = strings.TrimSpace(strings.NewReplacer(",", "", "，", "", " ", "").Replace(s))
	if s == "" {
		return 0, false
	}
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil
}

// depthOf reads a level cell: 1, 2 …; .1, ..2 (the number is the depth); 1.2.1 (its parts).
func depthOf(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	t := strings.TrimLeft(s, ".")
	if strings.Contains(t, ".") {
		n := 0
		for _, p := range strings.Split(t, ".") {
			if _, err := strconv.Atoi(p); err != nil {
				return 0, false
			}
			n++
		}
		return n, true
	}
	v, err := strconv.Atoi(t)
	return v, err == nil && v >= 0
}

// Imported is what an import read: the lines, the part when a row is the part itself, and what
// it could not use.
type Imported struct {
	Lines    []BOMLine `json:"lines"`
	Part     string    `json:"part,omitempty"`
	Name     string    `json:"name,omitempty"`
	Problems []string  `json:"problems,omitempty"`
	Columns  []string  `json:"columns"`
}

// ReadBOM reads a spreadsheet's rows into BOM lines.
func ReadBOM(rows [][]string) (*Imported, error) {
	head := -1
	col := map[string]int{}
	for i, r := range rows {
		if i > 30 {
			break
		}
		m := map[string]int{}
		for j, h := range r {
			if c := columnOf(h); c != "" {
				if _, dup := m[c]; !dup {
					m[c] = j
				}
			}
		}
		_, qty := m["qty"]
		_, code := m["code"]
		_, name := m["name"]
		if qty && (code || name) {
			head, col = i, m
			break
		}
	}
	if head < 0 {
		return nil, &Problems{[]string{"找不到表头：要有「用量」列，和「物料编码」或「名称」列"}}
	}
	out := &Imported{Lines: []BOMLine{}}
	for _, c := range columnOrder {
		if j, ok := col[c]; ok {
			out.Columns = append(out.Columns, rows[head][j])
		}
	}
	cell := func(r []string, c string) string {
		j, ok := col[c]
		if !ok || j >= len(r) {
			return ""
		}
		return strings.TrimSpace(r[j])
	}
	percent := strings.Contains(cell(rows[head], "scrap"), "%")
	// the line last seen at each depth, and by code: where the next one goes
	at := map[int]string{}
	byCode := map[string]string{}
	base := -1
	for i, r := range rows[head+1:] {
		row := head + i + 2
		code, name := cell(r, "code"), cell(r, "name")
		if code == "" && name == "" {
			continue
		}
		depth, hasDepth := depthOf(cell(r, "level"))
		if _, ok := col["level"]; ok && !hasDepth && cell(r, "level") != "" {
			out.Problems = append(out.Problems, fmt.Sprintf("第 %d 行的层级「%s」看不懂，放在了第一层", row, cell(r, "level")))
		}
		if hasDepth && base < 0 {
			base = depth
		}
		if hasDepth && depth == 0 {
			// the part itself
			if out.Part == "" {
				out.Part, out.Name = code, name
			}
			continue
		}
		qty, ok := number(cell(r, "qty"))
		if !ok || qty <= 0 {
			out.Problems = append(out.Problems, fmt.Sprintf("第 %d 行（%s）的用量「%s」不对，没导入", row, firstOf(name, code), cell(r, "qty")))
			continue
		}
		l := BOMLine{ID: fmt.Sprintf("L%d", row), Code: code, Name: name, Kind: kindOf(cell(r, "kind")), Spec: cell(r, "spec"), Grade: cell(r, "grade"),
			Qty: qty, Unit: cell(r, "unit"), Op: cell(r, "op"), Note: cell(r, "note")}
		if sc := cell(r, "scrap"); sc != "" {
			v, ok := number(strings.TrimSuffix(sc, "%"))
			switch {
			case !ok:
				out.Problems = append(out.Problems, fmt.Sprintf("第 %d 行的损耗「%s」看不懂，按 0 算", row, sc))
			case strings.HasSuffix(sc, "%") || percent || v >= 1:
				l.Scrap = v
			default:
				l.Scrap = v * 100 // 0.03 is 3%
			}
		}
		if l.Kind == "" {
			l.Kind = guessKind(l)
		}
		switch {
		case hasDepth:
			d := depth
			if base > 0 {
				d = depth - base + 1
			}
			d = max(d, 1)
			for d > 1 && at[d-1] == "" {
				d--
			}
			l.Parent = at[d-1]
			at[d] = l.ID
			for k := range at {
				if k > d {
					delete(at, k)
				}
			}
		case cell(r, "parent") != "":
			p := cell(r, "parent")
			if id, ok := byCode[strings.ToUpper(p)]; ok {
				l.Parent = id
			} else if out.Part == "" {
				out.Part = p // the parent no row is: the part itself
			} else if !strings.EqualFold(p, out.Part) {
				out.Problems = append(out.Problems, fmt.Sprintf("第 %d 行的上级「%s」在它前面没出现过，放在了第一层", row, p))
			}
		}
		if code != "" {
			byCode[strings.ToUpper(code)] = l.ID
		}
		out.Lines = append(out.Lines, l)
	}
	if len(out.Lines) == 0 {
		out.Problems = append(out.Problems, "表头下面没有读到物料")
	}
	return out, nil
}

func firstOf(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// guessKind is what a line is when the sheet does not say: steel with a grade is raw material,
// a GB/T or a thread size a standard part, by the kg a consumable; else bought in.
func guessKind(l BOMLine) string {
	u := strings.ToLower(l.Unit)
	switch {
	case l.Grade != "" && (u == "kg" || u == "t" || strings.Contains(l.Name, "钢") || strings.Contains(l.Name, "棒")):
		return Raw
	case strings.Contains(l.Spec, "GB") || strings.Contains(l.Name, "螺栓") || strings.Contains(l.Name, "螺母") || strings.Contains(l.Name, "螺塞") ||
		strings.Contains(l.Name, "垫圈") || strings.Contains(l.Name, "销") || strings.Contains(l.Name, "键") || strings.Contains(l.Name, "挡圈"):
		return Std
	case u == "kg" || u == "l" || u == "升":
		return Aux
	}
	return Buy
}

// ImportBOM reads a spreadsheet into a new draft revision of a part (b's part, name and revision;
// or the part the sheet's top row names).
func (s *Service) ImportBOM(company, who string, b BOM, rows [][]string) (*BOM, *Imported, error) {
	im, err := ReadBOM(rows)
	if err != nil {
		return nil, nil, err
	}
	b.Part = strings.TrimSpace(b.Part)
	if b.Part == "" {
		b.Part = im.Part
	} else if im.Part != "" && !strings.EqualFold(b.Part, im.Part) {
		return nil, nil, &Problems{[]string{fmt.Sprintf("表里写的零件是 %s，上面填的是 %s：对一下是哪个零件的 BOM", im.Part, b.Part)}}
	}
	if strings.TrimSpace(b.Name) == "" {
		b.Name = im.Name
		if b.Name == "" {
			b.Name = b.Part
		}
	}
	b.ID, b.Lines, b.Source = "", im.Lines, "导入"
	saved, err := s.SaveBOM(company, who, b)
	return saved, im, err
}
