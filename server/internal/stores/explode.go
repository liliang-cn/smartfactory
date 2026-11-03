package stores

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"
)

// Exploding a BOM: for so many of a part, how much of every component goes in, down to what is
// bought or cut from bar (the leaves), and how that stands against what the stores hold.

// Row is a line of an exploded BOM: its depth, how much goes into one of the part, and how much
// into all of them.
type Row struct {
	Level int `json:"level"`
	BOMLine
	// Per is how much goes into one of the part, scrap included; Total into all of them.
	Per   float64 `json:"per"`
	Total float64 `json:"total"`
	// Leaf: nothing below it (bought, or raw material); Sub the part number whose own BOM it opens into.
	Leaf bool   `json:"leaf"`
	Sub  string `json:"sub,omitempty"`
	Rev  string `json:"rev,omitempty"`
}

// Need is what of one leaf item is needed, and how the stores stand for it.
type Need struct {
	Code  string `json:"code"`
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Spec  string `json:"spec,omitempty"`
	Grade string `json:"grade,omitempty"`
	Unit  string `json:"unit"`
	// Need is how much is needed, in Unit.
	Need float64 `json:"need"`
	// Item is the store item it is kept as ("" when the stores keep none); Stock what they hold,
	// in Unit; StoreUnit theirs when it is another (t where the BOM says kg).
	Item      string  `json:"item,omitempty"`
	ItemName  string  `json:"itemName,omitempty"`
	Stock     float64 `json:"stock"`
	StoreUnit string  `json:"storeUnit,omitempty"`
	// Have is what of the stock goes to it (orders ahead take theirs first); Short what is missing.
	Have  float64 `json:"have"`
	Short float64 `json:"short"`
	// Problem: the stores keep none of it, or count it in a unit that does not convert.
	Problem string `json:"problem,omitempty"`
	// Ops are the operations it is used at.
	Ops []string `json:"ops,omitempty"`
}

// Explosion is a BOM exploded for a quantity.
type Explosion struct {
	Part   string `json:"part"`
	Name   string `json:"name"`
	Rev    string `json:"rev"`
	Status string `json:"status"`
	BOM    string `json:"bom"`
	// Effective and Drawing are the revision's.
	Effective string  `json:"effective,omitempty"`
	Drawing   string  `json:"drawing,omitempty"`
	Qty       float64 `json:"qty"`
	Unit      string  `json:"unit"`
	Rows      []Row   `json:"rows"`
	Needs     []Need  `json:"needs"`
	// Short is how many leaves are short; Unkept how many the stores keep none of.
	Short  int `json:"short"`
	Unkept int `json:"unkept"`
}

// unitGroups are the units that convert, with their size in the group's base (kg, m, L).
var unitGroups = []map[string]float64{
	{"kg": 1, "公斤": 1, "千克": 1, "t": 1000, "吨": 1000, "g": 0.001, "克": 0.001},
	{"m": 1, "米": 1, "mm": 0.001, "毫米": 0.001, "cm": 0.01, "厘米": 0.01},
	{"l": 1, "升": 1, "ml": 0.001, "毫升": 0.001},
}

// convert is how many of to one from is (1 kg is 0.001 t), and whether they convert at all.
func convert(from, to string) (float64, bool) {
	f, t := strings.ToLower(strings.TrimSpace(from)), strings.ToLower(strings.TrimSpace(to))
	if f == t {
		return 1, true
	}
	for _, g := range unitGroups {
		a, ok1 := g[f]
		b, ok2 := g[t]
		if ok1 && ok2 {
			return a / b, true
		}
	}
	return 0, false
}

// Explode explodes a part's revision in effect (or the revision rev names: an id, or a revision
// of the part) for qty of it.
func (s *Service) Explode(company, part, rev string, qty float64) (*Explosion, error) {
	all, err := s.boms(company)
	if err != nil {
		return nil, err
	}
	items, err := s.Items(company, "")
	if err != nil {
		return nil, err
	}
	b := pick(all, part, rev)
	if b == nil {
		return nil, ErrNotFound
	}
	if qty <= 0 || math.IsNaN(qty) || math.IsInf(qty, 0) {
		qty = 1
	}
	x := explode(all, *b, qty)
	x.Needs = needs(x.Rows)
	allot(x.Needs, items, map[string]float64{})
	count(x)
	return x, nil
}

func count(x *Explosion) {
	x.Short, x.Unkept = 0, 0
	for _, n := range x.Needs {
		if n.Short > 1e-9 {
			x.Short++
		}
		if n.Item == "" {
			x.Unkept++
		}
	}
}

// pick is the revision rev names of part (its id or revision), or part's revision in effect.
func pick(all []BOM, part, rev string) *BOM {
	for i, b := range all {
		if rev != "" && (b.ID == rev || strings.EqualFold(b.Part, part) && strings.EqualFold(b.Rev, rev)) {
			return &all[i]
		}
		if rev == "" && b.ID == part {
			return &all[i]
		}
	}
	if rev != "" {
		return nil
	}
	return active(all, part)
}

// explode walks a revision's tree for qty: a line with lines under it opens into them, a line
// whose code is a part with a revision in effect opens into that revision's lines.
func explode(all []BOM, b BOM, qty float64) *Explosion {
	x := &Explosion{Part: b.Part, Name: b.Name, Rev: b.Rev, Status: b.Status, BOM: b.ID, Effective: b.Effective, Drawing: b.Drawing, Qty: qty, Unit: b.Unit, Rows: []Row{}}
	var walk func(lines []BOMLine, parent string, level int, per float64, path []string)
	walk = func(lines []BOMLine, parent string, level int, per float64, path []string) {
		for _, l := range lines {
			if l.Parent != parent {
				continue
			}
			r := Row{Level: level, BOMLine: l, Per: round6(per * l.Qty * (1 + l.Scrap/100))}
			r.Total = round6(r.Per * qty)
			has := slices.ContainsFunc(lines, func(c BOMLine) bool { return c.Parent == l.ID })
			var sub *BOM
			if !has && level < 12 && !slices.ContainsFunc(path, func(p string) bool { return strings.EqualFold(p, l.Code) }) {
				sub = active(all, l.Code)
			}
			r.Leaf = !has && sub == nil
			if sub != nil {
				r.Sub, r.Rev = sub.Part, sub.Rev
			}
			x.Rows = append(x.Rows, r)
			switch {
			case has:
				walk(lines, l.ID, level+1, r.Per, path)
			case sub != nil:
				// a sub-assembly's BOM is per one of it, in its own unit
				walk(sub.Lines, "", level+1, r.Per, append(slices.Clone(path), sub.Part))
			}
		}
	}
	walk(b.Lines, "", 1, 1, []string{b.Part})
	return x
}

func round6(v float64) float64 { return math.Round(v*1e6) / 1e6 }

// needs adds up the leaves by item: the same bolt in two places is one need.
func needs(rows []Row) []Need {
	var out []Need
	for _, r := range rows {
		if !r.Leaf {
			continue
		}
		i := slices.IndexFunc(out, func(n Need) bool { return strings.EqualFold(n.Code, r.Code) })
		if i < 0 {
			out = append(out, Need{Code: r.Code, Name: r.Name, Kind: r.Kind, Spec: r.Spec, Grade: r.Grade, Unit: r.Unit})
			i = len(out) - 1
		}
		n := &out[i]
		f, ok := convert(r.Unit, n.Unit)
		if !ok {
			// the same code counted in another unit: kept apart
			out = append(out, Need{Code: r.Code, Name: r.Name, Kind: r.Kind, Spec: r.Spec, Grade: r.Grade, Unit: r.Unit})
			n, f = &out[len(out)-1], 1
		}
		n.Need = round6(n.Need + r.Total*f)
		if r.Op != "" && !slices.Contains(n.Ops, r.Op) {
			n.Ops = append(n.Ops, r.Op)
		}
	}
	slices.SortStableFunc(out, func(a, b Need) int { return cmp.Compare(kindOrder(a.Kind), kindOrder(b.Kind)) })
	return out
}

func kindOrder(k string) int {
	return slices.IndexFunc(LineKinds, func(x [2]string) bool { return x[0] == k })
}

// itemFor is the store item a leaf is kept as: by code; for raw material, by grade and size.
func itemFor(items []Item, n Need) *Item {
	for i, it := range items {
		if strings.EqualFold(it.Code, n.Code) {
			return &items[i]
		}
	}
	if n.Grade != "" {
		var hit *Item
		for i, it := range items {
			if !strings.EqualFold(it.Attrs["牌号"], n.Grade) {
				continue
			}
			if n.Spec != "" && !sameSize(it.Attrs["规格"], n.Spec) {
				continue
			}
			if hit != nil {
				return nil // two bars of the grade and no size to tell them apart
			}
			hit = &items[i]
		}
		return hit
	}
	return nil
}

// sameSize is whether two sizes are the same, written either way (φ80, Φ80, 80).
func sameSize(a, b string) bool {
	norm := func(s string) string {
		s = strings.ToLower(strings.Join(strings.Fields(s), ""))
		for _, p := range []string{"φ", "ф", "ø", "Φ", "d"} {
			s = strings.TrimPrefix(s, strings.ToLower(p))
		}
		return strings.TrimSuffix(s, "mm")
	}
	return norm(a) == norm(b)
}

// allot gives each need what the stores hold of it, less what was given out before (left: what
// is left of each item, in its own unit, kept between orders).
func allot(ns []Need, items []Item, taken map[string]float64) {
	for i := range ns {
		n := &ns[i]
		it := itemFor(items, *n)
		if it == nil {
			n.Problem = "库房没有建档"
			n.Short = n.Need
			continue
		}
		n.Item, n.ItemName = it.ID, it.Label()
		f, ok := convert(it.Unit, n.Unit)
		if !ok {
			n.Problem = fmt.Sprintf("库房按 %s 记，BOM 按 %s，换算不了", it.Unit, n.Unit)
			n.Short = n.Need
			continue
		}
		if it.Unit != n.Unit {
			n.StoreUnit = it.Unit
		}
		left := math.Max(0, it.Stock-taken[it.ID])
		n.Stock = round6(it.Stock * f)
		n.Have = round6(math.Min(n.Need, left*f))
		n.Short = round6(math.Max(0, n.Need-n.Have))
		taken[it.ID] += n.Have / f
	}
}

// Demand is an order the stores are to supply: what part and how many are still to make.
type Demand struct {
	No      string  `json:"no"`
	Part    string  `json:"part"`
	Product string  `json:"product,omitempty"`
	Qty     float64 `json:"qty"`
}

// Kit is how an order's materials stand: complete, short of some, or no BOM to tell.
type Kit struct {
	No   string  `json:"no"`
	Part string  `json:"part"`
	Qty  float64 `json:"qty"`
	// Rev is the BOM revision in effect it was exploded by ("" without one).
	Rev   string `json:"rev,omitempty"`
	Name  string `json:"name,omitempty"`
	Ready bool   `json:"ready"`
	// Needs are its leaves with what it gets of each; Short how many it is short of.
	Needs []Need `json:"needs"`
	Short int    `json:"short"`
}

// Shortage is what the open orders together lack of an item.
type Shortage struct {
	Need
	Orders []string `json:"orders"`
}

// Kitting is how the orders' materials stand, the orders given in the order they run: each takes
// what the stores hold before the next one does.
type Kitting struct {
	Orders []Kit      `json:"orders"`
	Short  []Shortage `json:"short"`
	// Totals is what the orders need together, of every item.
	Totals []Shortage `json:"totals"`
}

// Kit explodes the orders' parts by their revisions in effect and allots the stock to them in turn.
func (s *Service) Kit(company string, orders []Demand) (*Kitting, error) {
	all, err := s.boms(company)
	if err != nil {
		return nil, err
	}
	items, err := s.Items(company, "")
	if err != nil {
		return nil, err
	}
	out := &Kitting{Orders: []Kit{}, Short: []Shortage{}, Totals: []Shortage{}}
	taken, shortOf := map[string]float64{}, map[string][]string{}
	add := func(list *[]Shortage, n Need, no string, need, short float64) {
		i := slices.IndexFunc(*list, func(x Shortage) bool { return strings.EqualFold(x.Code, n.Code) && x.Unit == n.Unit })
		if i < 0 {
			*list = append(*list, Shortage{Need: Need{Code: n.Code, Name: n.Name, Kind: n.Kind, Spec: n.Spec, Grade: n.Grade, Unit: n.Unit,
				Item: n.Item, ItemName: n.ItemName, Stock: n.Stock, StoreUnit: n.StoreUnit, Problem: n.Problem}})
			i = len(*list) - 1
		}
		x := &(*list)[i]
		x.Need.Need = round6(x.Need.Need + need)
		x.Short = round6(x.Short + short)
		x.Have = round6(x.Need.Need - x.Short)
		x.Ops = n.Ops
		if !slices.Contains(x.Orders, no) {
			x.Orders = append(x.Orders, no)
		}
	}
	for _, d := range orders {
		k := Kit{No: d.No, Part: cmp.Or(d.Part, d.Product), Qty: d.Qty, Needs: []Need{}}
		b := active(all, d.Part)
		if b == nil && d.Product != "" {
			b = active(all, d.Product)
		}
		if b == nil || d.Qty <= 0 {
			if b != nil {
				k.Rev, k.Name, k.Ready = b.Rev, b.Name, true
			}
			out.Orders = append(out.Orders, k)
			continue
		}
		x := explode(all, *b, d.Qty)
		k.Rev, k.Name, k.Needs = b.Rev, b.Name, needs(x.Rows)
		allot(k.Needs, items, taken)
		for _, n := range k.Needs {
			add(&out.Totals, n, d.No, n.Need, n.Short)
			if n.Short > 1e-9 {
				k.Short++
				shortOf[strings.ToUpper(n.Code)+"|"+n.Unit] = append(shortOf[strings.ToUpper(n.Code)+"|"+n.Unit], d.No)
			}
		}
		k.Ready = k.Short == 0
		out.Orders = append(out.Orders, k)
	}
	// what is short: of every order's need, the orders that go without
	for _, t := range out.Totals {
		if t.Short > 1e-9 {
			t.Orders = shortOf[strings.ToUpper(t.Code)+"|"+t.Unit]
			out.Short = append(out.Short, t)
		}
	}
	return out, nil
}

// InEffect are the company's revisions in effect, each exploded for one of it.
func (s *Service) InEffect(company string) ([]Explosion, error) {
	all, err := s.boms(company)
	if err != nil {
		return nil, err
	}
	var out []Explosion
	for _, b := range all {
		if b.Status == Active {
			x := explode(all, b, 1)
			out = append(out, *x)
		}
	}
	return out, nil
}

// KindLabel is what a kind of line is called (自制件, 标准件 …).
func KindLabel(k string) string { return kindLabel(k) }

// Usage is where a component goes: into which part (by its revision in effect), how much per one of it.
type Usage struct {
	Part  string  `json:"part"`
	Name  string  `json:"name"`
	Rev   string  `json:"rev"`
	Code  string  `json:"code"`
	Item  string  `json:"item"`
	Kind  string  `json:"kind"`
	Per   float64 `json:"per"`
	Unit  string  `json:"unit"`
	Level int     `json:"level"`
	// Via is the sub-assembly it goes in through ("": straight into the part).
	Via string `json:"via,omitempty"`
	Op  string `json:"op,omitempty"`
}

// WhereUsed is every part whose revision in effect uses a component matching q (its code, name,
// spec or grade), at any depth.
func (s *Service) WhereUsed(company, q string) ([]Usage, error) {
	all, err := s.boms(company)
	if err != nil {
		return nil, err
	}
	out := []Usage{}
	if strings.TrimSpace(q) == "" {
		return out, nil
	}
	for _, b := range all {
		if b.Status != Active {
			continue
		}
		x := explode(all, b, 1)
		var via []string
		for _, r := range x.Rows {
			via = append(via[:min(len(via), r.Level-1)], r.Name)
			if !Matches(q, r.BOMLine) {
				continue
			}
			u := Usage{Part: b.Part, Name: b.Name, Rev: b.Rev, Code: r.Code, Item: r.Label(), Kind: r.Kind, Per: r.Per, Unit: r.Unit, Level: r.Level, Op: r.Op}
			if r.Level > 1 {
				u.Via = strings.Join(via[:r.Level-1], " / ")
			}
			out = append(out, u)
		}
	}
	return out, nil
}

// kindWords are what people call a kind of line when they ask about it.
var kindWords = map[string]string{Make: "自制件 半成品 锻件 总成", Buy: "外购件 采购件 外协", Std: "标准件 紧固件", Raw: "原材料 棒料 圆钢 钢材 坯料", Aux: "辅料 耗材"}

// Matches is whether a line answers a question's words: every word of q is in its code, name,
// spec, grade or what its kind is called ("40Cr 棒料" is a 40Cr bar).
func Matches(q string, l BOMLine) bool {
	text := strings.ToLower(strings.ReplaceAll(strings.Join([]string{l.Code, l.Name, l.Spec, l.Grade, kindLabel(l.Kind), kindWords[l.Kind]}, " "), " ", ""))
	words := strings.Fields(strings.ToLower(q))
	for _, w := range words {
		if !strings.Contains(text, w) {
			return false
		}
	}
	return len(words) > 0
}
