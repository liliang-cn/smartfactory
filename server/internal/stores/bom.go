package stores

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
	"unicode/utf8"
)

// A product's bill of materials (BOM): by part number, in revisions, each a tree of what goes
// into one of it — the forging and the bar it is cut from, the bought-in gear, the keys and plugs,
// the shot and the die lubricant — with how much per piece, the scrap allowed for, and the
// operation it is used at. One revision of a part is in effect at a time; putting a new one in
// effect writes an engineering change record (ECO) of what changed, why, and who approved it.
// A line whose code has a BOM of its own is a sub-assembly: exploding a BOM goes down into it.

// BOM statuses.
const (
	Draft   = "草稿"
	Active  = "生效"
	Retired = "停用"
)

// Line kinds, and what they are called.
const (
	Make = "make" // 自制件: forged, machined or assembled here
	Buy  = "buy"  // 外购件
	Std  = "std"  // 标准件
	Raw  = "raw"  // 原材料: bar, billet, ingot
	Aux  = "aux"  // 辅料: shot, lubricant, oil, packing
)

// LineKinds are the kinds of line in a BOM, in the order a person picks them.
var LineKinds = [][2]string{{Make, "自制件"}, {Buy, "外购件"}, {Std, "标准件"}, {Raw, "原材料"}, {Aux, "辅料"}}

func kindLabel(k string) string {
	for _, x := range LineKinds {
		if x[0] == k {
			return x[1]
		}
	}
	return k
}

// BOM is one revision of a part's bill of materials.
type BOM struct {
	ID   string `json:"id"`
	Part string `json:"part"`
	Name string `json:"name"`
	Rev  string `json:"rev"`
	// Drawing is the drawing revision it is made to.
	Drawing string `json:"drawing,omitempty"`
	Status  string `json:"status"`
	// Effective is the day it took (or takes) effect, YYYY-MM-DD.
	Effective string    `json:"effective,omitempty"`
	Unit      string    `json:"unit,omitempty"`
	Lines     []BOMLine `json:"lines"`
	Note      string    `json:"note,omitempty"`
	// Source: 手工 | 导入 | 研发; Project the development project it came from.
	Source  string `json:"source,omitempty"`
	Project string `json:"project,omitempty"`
	By      string `json:"by,omitempty"`
	// Released is when it was put in effect, ReleasedBy who approved it.
	Released   *time.Time `json:"released,omitempty"`
	ReleasedBy string     `json:"releasedBy,omitempty"`
	Created    time.Time  `json:"created"`
	Updated    time.Time  `json:"updated"`
}

// BOMLine is one component: what it is, how much of it goes into one of its parent.
type BOMLine struct {
	ID string `json:"id"`
	// Parent is the line it goes into ("": the part itself).
	Parent string `json:"parent,omitempty"`
	// Code is the material code (the store item's code); Name what it is called.
	Code string `json:"code"`
	Name string `json:"name"`
	Kind string `json:"kind"`
	// Spec is its size or standard (φ80, M10×1, GB/T 1099), Grade its material grade (42CrMo).
	Spec  string  `json:"spec,omitempty"`
	Grade string  `json:"grade,omitempty"`
	Qty   float64 `json:"qty"`
	Unit  string  `json:"unit"`
	// Scrap is the share lost on the way, in percent (下料损耗、烧损): what is needed is qty × (1 + scrap/100).
	Scrap float64 `json:"scrap,omitempty"`
	// Op is the operation or station it is used at (下料、热模锻、装配).
	Op   string `json:"op,omitempty"`
	Note string `json:"note,omitempty"`
}

// Label names a line: its name with its spec or grade.
func (l BOMLine) Label() string {
	var out []string
	if l.Grade != "" && !strings.Contains(l.Name, l.Grade) {
		out = append(out, l.Grade)
	}
	out = append(out, l.Name)
	if l.Spec != "" && !strings.Contains(l.Name, l.Spec) {
		out = append(out, l.Spec)
	}
	return strings.Join(out, " ")
}

// ECO is an engineering change: a part's BOM going from one revision to another.
type ECO struct {
	ID   string `json:"id"`
	Part string `json:"part"`
	Name string `json:"name"`
	// From and To are the revisions ("" From: the first one in effect); BOM the revision put in effect.
	From string `json:"from,omitempty"`
	To   string `json:"to"`
	BOM  string `json:"bom"`
	// DrawingFrom and DrawingTo are the drawing revisions before and after.
	DrawingFrom string   `json:"drawingFrom,omitempty"`
	DrawingTo   string   `json:"drawingTo,omitempty"`
	Changes     []string `json:"changes"`
	Why         string   `json:"why"`
	// Project is the development project that made the change (its id and part).
	Project string    `json:"project,omitempty"`
	By      string    `json:"by"`
	At      time.Time `json:"at"`
}

// ProductRef is a part number the company's lines make, as its config says.
type ProductRef struct {
	Part string `json:"part"`
	Name string `json:"name"`
	// Product is the product (family) key it belongs to, Material its grade; Weight and Input the
	// finished and the cut weight in kg.
	Product  string  `json:"product"`
	Family   string  `json:"family,omitempty"`
	Material string  `json:"material,omitempty"`
	Weight   float64 `json:"weight,omitempty"`
	Input    float64 `json:"input,omitempty"`
}

// PartRow is a part number in the BOM list: what its config says, and its revisions.
type PartRow struct {
	ProductRef
	// Active is the revision in effect ("": none); Drafts how many drafts it has; Lines how many lines the one in effect has.
	Active    string `json:"active,omitempty"`
	ActiveID  string `json:"activeId,omitempty"`
	Effective string `json:"effective,omitempty"`
	Drawing   string `json:"drawing,omitempty"`
	Drafts    int    `json:"drafts"`
	Revisions int    `json:"revisions"`
	Lines     int    `json:"lines"`
	// Listed: a part number the company's lines make (not only one with a BOM).
	Listed bool `json:"listed"`
}

// Parts are the company's part numbers with how their BOMs stand: those its lines make, then
// those only a BOM names (a sub-assembly, a part in development).
func (s *Service) Parts(company string) ([]PartRow, error) {
	all, err := s.boms(company)
	if err != nil {
		return nil, err
	}
	out := []PartRow{}
	at := map[string]int{}
	if s.Products != nil {
		for _, p := range s.Products(company) {
			if _, dup := at[strings.ToUpper(p.Part)]; dup || p.Part == "" {
				continue
			}
			at[strings.ToUpper(p.Part)] = len(out)
			out = append(out, PartRow{ProductRef: p, Listed: true})
		}
	}
	for _, b := range all {
		i, ok := at[strings.ToUpper(b.Part)]
		if !ok {
			i = len(out)
			at[strings.ToUpper(b.Part)] = i
			out = append(out, PartRow{ProductRef: ProductRef{Part: b.Part, Name: b.Name}})
		}
		r := &out[i]
		r.Revisions++
		switch b.Status {
		case Draft:
			r.Drafts++
		case Active:
			r.Active, r.ActiveID, r.Effective, r.Drawing, r.Lines = b.Rev, b.ID, b.Effective, b.Drawing, len(b.Lines)
		}
	}
	return out, nil
}

func (s *Service) boms(company string) ([]BOM, error) {
	out, err := getAll[BOM](s.db, `SELECT json FROM boms WHERE company = ?`, company)
	slices.SortFunc(out, func(a, b BOM) int { return cmp.Or(strings.Compare(a.Part, b.Part), revCompare(a.Rev, b.Rev)) })
	return out, err
}

// revCompare orders revisions A < B < … < Z < AA, and 2 < 10.
func revCompare(a, b string) int {
	if len(a) != len(b) {
		return cmp.Compare(len(a), len(b))
	}
	return strings.Compare(a, b)
}

// BOMs are the company's BOM revisions, by part and revision; of one part when part is not "".
func (s *Service) BOMs(company, part string) ([]BOM, error) {
	all, err := s.boms(company)
	if err != nil {
		return nil, err
	}
	out := []BOM{}
	for _, b := range all {
		if part == "" || strings.EqualFold(b.Part, part) {
			out = append(out, b)
		}
	}
	return out, nil
}

// BOMOf is a revision by id, or the revision in effect of a part number (or its latest when none is).
func (s *Service) BOMOf(company, id string) (*BOM, error) {
	all, err := s.boms(company)
	if err != nil {
		return nil, err
	}
	var latest *BOM
	for i, b := range all {
		if b.ID == id {
			return &b, nil
		}
		if strings.EqualFold(b.Part, id) {
			if b.Status == Active {
				return &b, nil
			}
			latest = &all[i]
		}
	}
	if latest != nil {
		return latest, nil
	}
	return nil, ErrNotFound
}

// active is the revision of a part in effect (nil: none).
func active(all []BOM, part string) *BOM {
	for i, b := range all {
		if b.Status == Active && strings.EqualFold(b.Part, part) {
			return &all[i]
		}
	}
	return nil
}

func nextRev(all []BOM, part string) string {
	letters, last := true, ""
	n := 0
	for _, b := range all {
		if !strings.EqualFold(b.Part, part) {
			continue
		}
		n++
		if len(b.Rev) != 1 || b.Rev[0] < 'A' || b.Rev[0] > 'Y' {
			letters = false
		}
		if last == "" || revCompare(b.Rev, last) > 0 {
			last = b.Rev
		}
	}
	switch {
	case n == 0:
		return "A"
	case letters:
		return string(rune(last[0] + 1))
	}
	return fmt.Sprintf("V%d", n+1)
}

// SaveBOM adds a draft revision or changes one; only drafts change, a revision in effect is
// changed by copying it to a new draft.
func (s *Service) SaveBOM(company, who string, b BOM) (*BOM, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return notify(s, company)(s.saveBOM(company, who, b))
}

func (s *Service) saveBOM(company, who string, b BOM) (*BOM, error) {
	b.Part, b.Name, b.Rev, b.Drawing, b.Note = strings.TrimSpace(b.Part), strings.TrimSpace(b.Name), strings.ToUpper(strings.TrimSpace(b.Rev)), strings.TrimSpace(b.Drawing), strings.TrimSpace(b.Note)
	all, err := s.boms(company)
	if err != nil {
		return nil, err
	}
	now := s.Now()
	if b.ID != "" {
		i := slices.IndexFunc(all, func(o BOM) bool { return o.ID == b.ID })
		if i < 0 {
			return nil, ErrNotFound
		}
		old := all[i]
		if old.Status != Draft {
			return nil, &Problems{[]string{fmt.Sprintf("%s %s 版已经%s，不能直接改：先「出新版本」再改", old.Part, old.Rev, old.Status)}}
		}
		b.Created, b.By, b.Status, b.Released, b.ReleasedBy = old.Created, old.By, Draft, nil, ""
		if b.Source == "" {
			b.Source = old.Source
		}
		if b.Project == "" {
			b.Project = old.Project
		}
	} else {
		b.ID, b.Created, b.By, b.Status, b.Released, b.ReleasedBy = newID("B"), now, who, Draft, nil, ""
		if b.Rev == "" {
			b.Rev = nextRev(all, b.Part)
		}
		if b.Source == "" {
			b.Source = "手工"
		}
	}
	var p []string
	if b.Part == "" {
		p = append(p, "填零件号")
	}
	if b.Name == "" {
		p = append(p, "填零件名称")
	}
	if utf8.RuneCountInString(b.Name) > 60 {
		p = append(p, "名称太长（最多 60 字）")
	}
	if b.Rev == "" || utf8.RuneCountInString(b.Rev) > 8 {
		p = append(p, "版本号写成 A、B 或 V2 这样（最多 8 个字）")
	}
	if slices.ContainsFunc(all, func(o BOM) bool { return o.ID != b.ID && strings.EqualFold(o.Part, b.Part) && o.Rev == b.Rev }) {
		p = append(p, fmt.Sprintf("%s 已经有 %s 版了", b.Part, b.Rev))
	}
	if b.Effective != "" {
		if _, err := time.Parse("2006-01-02", b.Effective); err != nil {
			p = append(p, "生效日期写成 2026-11-30 这样")
		}
	}
	if b.Unit = strings.TrimSpace(b.Unit); b.Unit == "" {
		b.Unit = "件"
	}
	lines, lp := checkLines(b.Part, b.Lines)
	b.Lines = lines
	p = append(p, lp...)
	if len(p) > 0 {
		return nil, &Problems{p}
	}
	b.Updated = now
	if err := s.putBOM(company, b); err != nil {
		return nil, err
	}
	return &b, nil
}

func (s *Service) putBOM(company string, b BOM) error {
	raw, err := jsonOf(b)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO boms (company, id, json) VALUES (?, ?, ?) ON CONFLICT (company, id) DO UPDATE SET json = excluded.json`, company, b.ID, raw)
	return err
}

// checkLines tidies a revision's lines and says what is wrong with them: each has a name and a
// quantity, a parent among the lines before it (or none), and is not the part itself; they come
// back in tree order, each under its parent.
func checkLines(part string, in []BOMLine) ([]BOMLine, []string) {
	var p []string
	ids := map[string]bool{}
	for i := range in {
		l := &in[i]
		l.Code, l.Name, l.Spec, l.Grade, l.Unit, l.Op, l.Note = strings.TrimSpace(l.Code), strings.TrimSpace(l.Name), strings.TrimSpace(l.Spec), strings.TrimSpace(l.Grade), strings.TrimSpace(l.Unit), strings.TrimSpace(l.Op), strings.TrimSpace(l.Note)
		l.ID = strings.TrimSpace(l.ID)
		if l.ID == "" || ids[l.ID] {
			l.ID = newID("L")
		}
		ids[l.ID] = true
	}
	for i := range in {
		l := &in[i]
		what := fmt.Sprintf("第 %d 行", i+1)
		if l.Name == "" && l.Code == "" {
			p = append(p, what+"要有物料名称或编码")
			continue
		}
		if l.Name == "" {
			l.Name = l.Code
		}
		if l.Code == "" {
			l.Code = l.Name
		}
		what += "（" + l.Name + "）"
		if strings.EqualFold(l.Code, part) {
			p = append(p, what+"就是这个零件自己")
		}
		if l.Qty <= 0 || math.IsNaN(l.Qty) || math.IsInf(l.Qty, 0) {
			p = append(p, what+"的用量要大于 0")
		}
		if l.Scrap < 0 || l.Scrap >= 100 || math.IsNaN(l.Scrap) {
			p = append(p, what+"的损耗率在 0 到 100% 之间")
		}
		if l.Unit == "" {
			l.Unit = "件"
		}
		if !slices.ContainsFunc(LineKinds, func(k [2]string) bool { return k[0] == l.Kind }) {
			l.Kind = Buy
		}
		if l.Parent != "" && !ids[l.Parent] {
			p = append(p, what+"的上级不在这张 BOM 里")
			l.Parent = ""
		}
		if l.Parent == l.ID {
			l.Parent = ""
		}
	}
	// tree order: each line under its parent, siblings as they came; a loop of parents is cut
	children := map[string][]int{}
	for i, l := range in {
		children[l.Parent] = append(children[l.Parent], i)
	}
	out := make([]BOMLine, 0, len(in))
	seen := map[string]bool{}
	var walk func(parent string, depth int)
	walk = func(parent string, depth int) {
		for _, i := range children[parent] {
			if seen[in[i].ID] {
				continue
			}
			seen[in[i].ID] = true
			if depth >= 8 {
				p = append(p, fmt.Sprintf("「%s」放得太深了（最多 8 层）", in[i].Name))
			}
			out = append(out, in[i])
			walk(in[i].ID, depth+1)
		}
	}
	walk("", 0)
	if len(out) < len(in) {
		p = append(p, "有几行的上级互相套成了圈")
	}
	if len(in) == 0 {
		p = append(p, "BOM 里还没有物料")
	}
	if len(in) > 2000 {
		p = append(p, "一张 BOM 最多 2000 行")
	}
	return out, p
}

// DeleteBOM removes a draft revision.
func (s *Service) DeleteBOM(company, id string) (*BOM, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.BOMOf(company, id)
	if err != nil || b.ID != id {
		return nil, ErrNotFound
	}
	if b.Status != Draft {
		return nil, &Problems{[]string{"只能删草稿；不用的版本请停用"}}
	}
	_, err = s.db.Exec(`DELETE FROM boms WHERE company = ? AND id = ?`, company, id)
	return notify(s, company)(b, err)
}

// CopyBOM starts a new draft revision of a part from one of its revisions.
func (s *Service) CopyBOM(company, who, id string) (*BOM, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.BOMOf(company, id)
	if err != nil {
		return nil, err
	}
	n := *b
	n.ID, n.Rev, n.Effective, n.Source, n.Project = "", "", "", "手工", ""
	n.Lines = slices.Clone(b.Lines)
	n.Note = fmt.Sprintf("由 %s 版复制", b.Rev)
	return notify(s, company)(s.saveBOM(company, who, n))
}

// Release is how a revision is put in effect: why, from what day, and with which drawing.
type Release struct {
	Why       string `json:"why"`
	Effective string `json:"effective,omitempty"`
	Drawing   string `json:"drawing,omitempty"`
	// Project is the development project the change comes from.
	Project string `json:"project,omitempty"`
}

// ReleaseBOM puts a draft revision in effect, approved by who: the part's revision in effect
// until now is retired, and an ECO records what changed and why.
func (s *Service) ReleaseBOM(company, who, id string, r Release) (*BOM, *ECO, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, e, err := s.releaseBOM(company, who, id, r)
	if err == nil && s.BOMChanged != nil {
		s.BOMChanged(company)
	}
	return b, e, err
}

// notify passes a change's result on, telling BOMChanged when it went through.
func notify(s *Service, company string) func(*BOM, error) (*BOM, error) {
	return func(b *BOM, err error) (*BOM, error) {
		if err == nil && s.BOMChanged != nil {
			s.BOMChanged(company)
		}
		return b, err
	}
}

func (s *Service) releaseBOM(company, who, id string, r Release) (*BOM, *ECO, error) {
	all, err := s.boms(company)
	if err != nil {
		return nil, nil, err
	}
	i := slices.IndexFunc(all, func(o BOM) bool { return o.ID == id })
	if i < 0 {
		return nil, nil, ErrNotFound
	}
	b := all[i]
	prev := active(all, b.Part)
	r.Why, r.Drawing = strings.TrimSpace(r.Why), strings.TrimSpace(r.Drawing)
	var p []string
	if b.Status != Draft {
		p = append(p, fmt.Sprintf("%s 版已经%s了", b.Rev, b.Status))
	}
	if r.Why == "" && prev != nil {
		p = append(p, "写一下为什么改（客户图纸改版、工艺优化、换供应商 …）")
	}
	if r.Effective == "" {
		r.Effective = s.Now().In(cst).Format("2006-01-02")
	} else if _, err := time.Parse("2006-01-02", r.Effective); err != nil {
		p = append(p, "生效日期写成 2026-11-30 这样")
	}
	if len(p) > 0 {
		return nil, nil, &Problems{p}
	}
	if r.Why == "" {
		r.Why = "首次发布"
	}
	now := s.Now()
	e := ECO{ID: newID("ECO"), Part: b.Part, Name: b.Name, To: b.Rev, BOM: b.ID, DrawingTo: cmp.Or(r.Drawing, b.Drawing), Why: r.Why, Project: cmp.Or(r.Project, b.Project), By: who, At: now}
	if prev != nil {
		e.From, e.DrawingFrom = prev.Rev, prev.Drawing
		e.Changes = diffLines(prev.Lines, b.Lines)
		prev.Status, prev.Updated = Retired, now
		if err := s.putBOM(company, *prev); err != nil {
			return nil, nil, err
		}
	} else {
		e.Changes = []string{fmt.Sprintf("%d 行物料", len(b.Lines))}
		if e.DrawingTo != "" {
			e.Changes[0] = fmt.Sprintf("按图纸 %s 版，%d 行物料", e.DrawingTo, len(b.Lines))
		}
	}
	if prev != nil && e.DrawingFrom != e.DrawingTo && e.DrawingTo != "" {
		e.Changes = append([]string{fmt.Sprintf("图纸 %s → %s 版", cmp.Or(e.DrawingFrom, "无"), e.DrawingTo)}, e.Changes...)
	}
	if len(e.Changes) == 0 {
		e.Changes = []string{"物料没有变化"}
	}
	b.Status, b.Effective, b.Released, b.ReleasedBy, b.Updated = Active, r.Effective, &now, who, now
	if r.Drawing != "" {
		b.Drawing = r.Drawing
	}
	if e.Project != "" {
		b.Project = e.Project
	}
	if err := s.putBOM(company, b); err != nil {
		return nil, nil, err
	}
	raw, err := jsonOf(e)
	if err != nil {
		return nil, nil, err
	}
	if _, err := s.db.Exec(`INSERT INTO ecos (company, id, part, at, json) VALUES (?,?,?,?,?)`, company, e.ID, strings.ToUpper(e.Part), ms(e.At), raw); err != nil {
		return nil, nil, err
	}
	return &b, &e, nil
}

// RetireBOM takes a revision in effect out of it: the part has none in effect until another is released.
func (s *Service) RetireBOM(company, who, id string) (*BOM, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, err := s.BOMOf(company, id)
	if err != nil || b.ID != id {
		return nil, ErrNotFound
	}
	if b.Status != Active {
		return nil, &Problems{[]string{"只有生效的版本才能停用"}}
	}
	b.Status, b.Updated = Retired, s.Now()
	return notify(s, company)(b, s.putBOM(company, *b))
}

// ECOs are the company's engineering changes, the newest first: of one part when part is not "".
func (s *Service) ECOs(company, part string, limit int) ([]ECO, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	if part != "" {
		return orEmpty(getAll[ECO](s.db, `SELECT json FROM ecos WHERE company = ? AND part = ? ORDER BY at DESC LIMIT ?`, company, strings.ToUpper(part), limit))
	}
	return orEmpty(getAll[ECO](s.db, `SELECT json FROM ecos WHERE company = ? ORDER BY at DESC LIMIT ?`, company, limit))
}

func orEmpty[T any](v []T, err error) ([]T, error) {
	if v == nil {
		v = []T{}
	}
	return v, err
}

// diffLines says what changed from one revision's lines to another's, line by line: added,
// removed, quantity, scrap, operation, spec or grade changed. A line is the same line when it has
// the same code under a parent of the same code.
func diffLines(old, cur []BOMLine) []string {
	key := func(ls []BOMLine) (map[string]BOMLine, []string) {
		code := map[string]string{}
		for _, l := range ls {
			code[l.ID] = l.Code
		}
		m, order := map[string]BOMLine{}, []string{}
		for _, l := range ls {
			k := strings.ToUpper(code[l.Parent] + "/" + l.Code)
			m[k] = l
			order = append(order, k)
		}
		return m, order
	}
	a, ao := key(old)
	b, bo := key(cur)
	amount := func(l BOMLine) string { return num(l.Qty) + " " + l.Unit }
	var out []string
	for _, k := range bo {
		n := b[k]
		o, ok := a[k]
		if !ok {
			out = append(out, fmt.Sprintf("加 %s %s", n.Label(), amount(n)))
			continue
		}
		var ch []string
		if o.Qty != n.Qty || o.Unit != n.Unit {
			ch = append(ch, fmt.Sprintf("用量 %s → %s", amount(o), amount(n)))
		}
		if o.Scrap != n.Scrap {
			ch = append(ch, fmt.Sprintf("损耗 %s%% → %s%%", num(o.Scrap), num(n.Scrap)))
		}
		if o.Spec != n.Spec {
			ch = append(ch, fmt.Sprintf("规格 %s → %s", cmp.Or(o.Spec, "无"), cmp.Or(n.Spec, "无")))
		}
		if o.Grade != n.Grade {
			ch = append(ch, fmt.Sprintf("牌号 %s → %s", cmp.Or(o.Grade, "无"), cmp.Or(n.Grade, "无")))
		}
		if o.Op != n.Op {
			ch = append(ch, fmt.Sprintf("工序 %s → %s", cmp.Or(o.Op, "无"), cmp.Or(n.Op, "无")))
		}
		if o.Name != n.Name {
			ch = append(ch, fmt.Sprintf("名称 %s → %s", o.Name, n.Name))
		}
		if len(ch) > 0 {
			out = append(out, n.Label()+" "+strings.Join(ch, "，"))
		}
	}
	for _, k := range ao {
		if _, ok := b[k]; !ok {
			out = append(out, fmt.Sprintf("去掉 %s %s", a[k].Label(), amount(a[k])))
		}
	}
	return out
}

var cst = time.FixedZone("CST", 8*3600)
