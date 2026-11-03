package scoped

import (
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strings"
)

// Preview is what a change does, said before it is saved.
type Preview struct {
	// Changes are the changes in words, one a line:
	// 产线「曲轴 2 线」工位「锻压」：节拍 45 s → 40 s
	Changes []string `json:"changes"`
	// Tags are the machine tags (位号) that change or go away.
	Tags []TagChange `json:"tags"`
	// Added are the tags of new machines.
	Added []string `json:"added"`
}

// TagChange is a machine whose tag changes (To) or that goes away (To empty).
type TagChange struct {
	Plant string `json:"plant"`
	From  string `json:"from"`
	To    string `json:"to,omitempty"`
	Name  string `json:"name"`
	Line  string `json:"line"`
	// Attached is what is filed under the old tag (devices, check forms filled in …), in words.
	Attached []string `json:"attached,omitempty"`
}

// Describe is what changing base into prop does.
func Describe(base, prop *Model) Preview {
	pv := Preview{Changes: []string{}, Tags: []TagChange{}, Added: []string{}}
	add := func(format string, a ...any) { pv.Changes = append(pv.Changes, fmt.Sprintf(format, a...)) }
	for pi := 0; pi < min(len(base.Plants), len(prop.Plants)); pi++ {
		bp, pp := base.plantCfg(pi), prop.plantCfg(pi)
		title := "工厂「" + base.Plants[pi].Name + "」"
		for _, k := range []string{"name", "hall", "badge", "warehouse", "finishedGoods"} {
			if a, b := show(bp[k]), show(pp[k]); a != b {
				add("%s：%s %s → %s", title, label(k), a, b)
			}
		}
		describeAreas(base.Plants[pi], prop.Plants[pi], bp, pp, add)
		describeLines(base, prop, pi, &pv, add)
	}
	return pv
}

func describeAreas(b, p Plant, bp, pp map[string]any, add func(string, ...any)) {
	// a plant without areas has one, its hall: its name is the hall's, said with the plant
	if len(arr(bp["areas"])) == 0 && len(arr(pp["areas"])) == 0 {
		return
	}
	match := pairUp(len(b.Areas), len(p.Areas),
		func(i, j int) bool { return b.Areas[i].Code == p.Areas[j].Code },
		func(i, j int) bool { return b.Areas[i].Name == p.Areas[j].Name })
	for i, a := range b.Areas {
		j, ok := match[i]
		if !ok {
			add("去掉车间「%s」", a.Name)
			continue
		}
		n := p.Areas[j]
		t := "车间「" + a.Name + "」"
		if a.Name != n.Name {
			add("%s：名称 %s → %s", t, a.Name, n.Name)
		}
		if a.Code != n.Code {
			add("%s：编码 %s → %s", t, a.Code, n.Code)
		}
		if a.Note != n.Note {
			add("%s：备注 %s → %s", t, show(a.Note), show(n.Note))
		}
	}
	taken := map[int]bool{}
	var before, after []string
	for i := range b.Areas {
		if j, ok := match[i]; ok {
			taken[j] = true
			before = append(before, p.Areas[j].Name)
		}
	}
	for j, a := range p.Areas {
		if taken[j] {
			after = append(after, a.Name)
		} else {
			add("新增车间「%s」", a.Name)
		}
	}
	if !slices.Equal(before, after) {
		add("车间的顺序：%s → %s", strings.Join(before, "、"), strings.Join(after, "、"))
	}
}

// describeLines says what changes on plant pi's lines, and collects the tags that move.
func describeLines(base, prop *Model, pi int, pv *Preview, add func(string, ...any)) {
	bl, pl := base.lineCfgs(pi), prop.lineCfgs(pi)
	var match map[int]int
	if len(bl) == len(pl) {
		match = map[int]int{}
		for i := range bl {
			match[i] = i
		}
	} else {
		match = pairUp(len(bl), len(pl), func(i, j int) bool { return str(obj(bl[i])["name"]) == str(obj(pl[j])["name"]) }, nil)
	}
	// a line's area, by its code; the areas of base paired with those of prop as their diff does
	ba, pa := base.Plants[pi].Areas, prop.Plants[pi].Areas
	areas := pairUp(len(ba), len(pa), func(i, j int) bool { return ba[i].Code == pa[j].Code }, func(i, j int) bool { return ba[i].Name == pa[j].Name })
	areaOf := func(m *Model, ci int) (int, string) {
		if ls := m.laid(pi, ci); len(ls) > 0 {
			all := m.Plants[pi].Areas
			if k := slices.IndexFunc(all, func(a Area) bool { return a.Code == ls[0].Area }); k >= 0 {
				return k, all[k].Name
			}
		}
		return -1, ""
	}
	plant := base.Plants[pi].ID
	gone := func(m *Model, ci int) {
		for _, l := range m.laid(pi, ci) {
			for _, st := range l.Stations {
				pv.Tags = append(pv.Tags, TagChange{Plant: plant, From: st.ID, Name: st.Name, Line: l.Name})
			}
		}
	}
	for i := range bl {
		j, ok := match[i]
		if !ok {
			add("去掉产线%s", base.lineTitle(pi, i))
			gone(base, i)
			continue
		}
		b, p := obj(bl[i]), obj(pl[j])
		t := "产线" + base.lineTitle(pi, i)
		for _, k := range []string{"name", "note", "product", "area", "repeat", "speed", "beltHeight", "input"} {
			a, c := show(b[k]), show(p[k])
			if k == "area" {
				ai, an := areaOf(base, i)
				pj, pn := areaOf(prop, j)
				if to, ok := areas[ai]; ok && to == pj {
					continue // the same area, perhaps renamed
				}
				a, c = an, pn
			}
			if a != c {
				add("%s：%s %s → %s", t, label(k), a+unit(k, a), c+unit(k, c))
			}
		}
		describeSteps(base, prop, pi, i, j, t, pv, add)
	}
	taken := map[int]bool{}
	for _, j := range match {
		taken[j] = true
	}
	for j := range pl {
		if !taken[j] {
			add("新增产线%s", prop.lineTitle(pi, j))
			for _, l := range prop.laid(pi, j) {
				for _, st := range l.Stations {
					pv.Added = append(pv.Added, st.ID)
				}
			}
		}
	}
}

// describeSteps compares the steps of line entry i (base) and j (prop).
func describeSteps(base, prop *Model, pi, i, j int, title string, pv *Preview, add func(string, ...any)) {
	bs, ps := arr(obj(base.lineCfgs(pi)[i])["steps"]), arr(obj(prop.lineCfgs(pi)[j])["steps"])
	key := func(s any) string { return str(obj(s)["machine"]) }
	bst := func(si int) Station { st, _ := base.station(pi, i, si); return st }
	pst := func(si int) Station { st, _ := prop.station(pi, j, si); return st }
	// the same kind and name first (another press put in front of one keeps the old one its
	// own), then what is left by kind
	matched := map[int]int{}
	taken := map[int]bool{}
	for _, pr := range lcs(len(bs), len(ps), func(a, b int) bool { return key(bs[a]) == key(ps[b]) && bst(a).Name == pst(b).Name }) {
		matched[pr[0]], taken[pr[1]] = pr[1], true
	}
	var ra, rb []int
	for a := range bs {
		if _, ok := matched[a]; !ok {
			ra = append(ra, a)
		}
	}
	for b := range ps {
		if !taken[b] {
			rb = append(rb, b)
		}
	}
	for _, pr := range lcs(len(ra), len(rb), func(x, y int) bool { return key(bs[ra[x]]) == key(ps[rb[y]]) }) {
		matched[ra[pr[0]]], taken[rb[pr[1]]] = rb[pr[1]], true
	}
	moved := map[int]bool{}
	// a step taken out and put back elsewhere (same kind, same name): moved
	for a := range bs {
		if _, ok := matched[a]; ok {
			continue
		}
		for b := range ps {
			if !taken[b] && key(bs[a]) == key(ps[b]) && bst(a).Name == pst(b).Name {
				matched[a], taken[b], moved[a] = b, true, true
				break
			}
		}
	}
	// one step swapped for another kind in the same place: its kind changed
	kind := map[int]bool{}
	for a := range bs {
		if _, ok := matched[a]; ok {
			continue
		}
		if a < len(ps) && !taken[a] && prevMatched(matched, a) {
			matched[a], taken[a], kind[a] = a, true, true
		}
	}
	after := func(b int) string {
		if b == 0 {
			return "排在最前"
		}
		return "排在「" + pst(b-1).Name + "」之后"
	}
	for a := range bs {
		o := bst(a)
		b, ok := matched[a]
		if !ok {
			add("%s：去掉工位「%s」（%s）", title, o.Name, o.ID)
			continue
		}
		n := pst(b)
		t := title + "工位「" + o.Name + "」"
		if kind[a] {
			add("%s：设备种类 %s → %s", t, o.Label, n.Label)
		}
		if moved[a] {
			add("%s：挪到新位置，%s", t, after(b))
		}
		if o.Name != n.Name {
			add("%s：名称 %s → %s", t, o.Name, n.Name)
		}
		if o.Cycle != n.Cycle {
			add("%s：节拍 %s s → %s s", t, fmtNum(o.Cycle), fmtNum(n.Cycle))
		}
		if o.Warn != n.Warn {
			add("%s：报警温度 %s°C → %s°C", t, fmtNum(o.Warn), fmtNum(n.Warn))
		}
		if o.Trip != n.Trip {
			add("%s：停机温度 %s°C → %s°C", t, fmtNum(o.Trip), fmtNum(n.Trip))
		}
		var other []string
		for _, k := range keysOf(obj(bs[a]), obj(ps[b])) {
			if slices.Contains([]string{"machine", "name", "cycle", "thermal"}, k) {
				continue
			}
			if !reflect.DeepEqual(obj(bs[a])[k], obj(ps[b])[k]) {
				other = append(other, label(k))
			}
		}
		if len(other) > 0 {
			add("%s：%s有变化", t, strings.Join(other, "、"))
		}
	}
	for b := range ps {
		if !taken[b] {
			n := pst(b)
			add("%s：新增工位「%s」，%s（%s）", title, n.Name, after(b), n.ID)
		}
	}
	// tags, repeat by repeat of the line
	bl, pl := base.laid(pi, i), prop.laid(pi, j)
	for r := range bl {
		if r >= len(pl) {
			for _, st := range bl[r].Stations {
				pv.Tags = append(pv.Tags, TagChange{Plant: base.Plants[pi].ID, From: st.ID, Name: st.Name, Line: bl[r].Name})
			}
			continue
		}
		for a, o := range bl[r].Stations {
			b, ok := matched[a]
			if !ok {
				pv.Tags = append(pv.Tags, TagChange{Plant: base.Plants[pi].ID, From: o.ID, Name: o.Name, Line: bl[r].Name})
			} else if b < len(pl[r].Stations) && pl[r].Stations[b].ID != o.ID {
				pv.Tags = append(pv.Tags, TagChange{Plant: base.Plants[pi].ID, From: o.ID, To: pl[r].Stations[b].ID, Name: o.Name, Line: bl[r].Name})
			}
		}
	}
	for r := range pl {
		for b, n := range pl[r].Stations {
			if !taken[b] || r >= len(bl) {
				pv.Added = append(pv.Added, n.ID)
			}
		}
	}
}

// prevMatched: the step before a is matched to the step before a on the other side (or a is first).
func prevMatched(m map[int]int, a int) bool {
	if a == 0 {
		return true
	}
	b, ok := m[a-1]
	return ok && b == a-1
}

// lcs pairs up the longest common subsequence of two lists, by indexes.
func lcs(n, m int, eq func(a, b int) bool) [][2]int {
	t := make([][]int, n+1)
	for i := range t {
		t[i] = make([]int, m+1)
	}
	for a := n - 1; a >= 0; a-- {
		for b := m - 1; b >= 0; b-- {
			if eq(a, b) {
				t[a][b] = t[a+1][b+1] + 1
			} else {
				t[a][b] = max(t[a+1][b], t[a][b+1])
			}
		}
	}
	var out [][2]int
	for a, b := 0, 0; a < n && b < m; {
		switch {
		case eq(a, b) && t[a][b] == t[a+1][b+1]+1:
			out = append(out, [2]int{a, b})
			a, b = a+1, b+1
		case t[a+1][b] >= t[a][b+1]:
			a++
		default:
			b++
		}
	}
	return out
}

// pairUp matches the items of two lists by the first test, then what is left by the second.
func pairUp(n, m int, first, second func(i, j int) bool) map[int]int {
	out := map[int]int{}
	taken := map[int]bool{}
	for _, test := range []func(i, j int) bool{first, second} {
		if test == nil {
			continue
		}
		for i := 0; i < n; i++ {
			if _, ok := out[i]; ok {
				continue
			}
			for j := 0; j < m; j++ {
				if !taken[j] && test(i, j) {
					out[i], taken[j] = j, true
					break
				}
			}
		}
	}
	return out
}

func keysOf(a, b map[string]any) []string {
	set := map[string]bool{}
	for k := range a {
		set[k] = true
	}
	for k := range b {
		set[k] = true
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// show writes a config value for a person: （空） for nothing.
func show(v any) string {
	switch t := v.(type) {
	case nil:
		return "（空）"
	case string:
		if strings.TrimSpace(t) == "" {
			return "（空）"
		}
		return t
	case float64:
		return fmtNum(t)
	default:
		return fmt.Sprint(t)
	}
}

func unit(k, v string) string {
	if v == "（空）" {
		return ""
	}
	switch k {
	case "speed":
		return " m/s"
	case "beltHeight":
		return " m"
	}
	return ""
}
