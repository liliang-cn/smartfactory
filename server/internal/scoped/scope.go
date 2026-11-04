package scoped

import (
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
)

// Node is the part of the company picked: a plant by id, an area by its code, a line by its id
// (L3) or a machine by its tag (HP-205), each in a plant.
type Node struct {
	Kind  string `json:"kind"` // plant | area | line | machine
	Plant string `json:"plant"`
	Area  string `json:"area,omitempty"`
	Line  string `json:"line,omitempty"`
	Asset string `json:"asset,omitempty"`
	// Single: change only this copy of a repeated line (it is split out first, see Split)
	Single bool `json:"single,omitempty"`
}

// Scope is a node resolved in a model: where in the config its subtree is.
type Scope struct {
	Node Node `json:"node"`
	// Title names it: 工厂「一厂」, 车间「压铸车间」, 产线「曲轴 2 线」, 设备 HP-205「锻压」
	Title string `json:"title"`
	// Path is where its subtree is in the config: plants/0/lines/1/steps/3
	Path string `json:"path"`
	// Plant is the plant's index; Line the line entry's (line, machine); Step the step's (machine)
	Plant int `json:"plant"`
	Line  int `json:"line"`
	Step  int `json:"step"`
	// AreaIndex is the area's place in the plant's areas, -1 when it is the plant's one hall
	AreaIndex int `json:"areaIndex"`
	// AreaLines are the line entries standing in the area
	AreaLines []int `json:"areaLines,omitempty"`
	// Shared: the line entry builds several lines (repeat), which a change to it changes together
	Shared string `json:"shared,omitempty"`
	// Split: only this copy of a repeated line is changed — it was made a line entry of its own
	// first (so Line is that entry); says so in words
	Split string `json:"split,omitempty"`
	// Lines is how many line entries the plant has
	Lines int `json:"-"`
}

// Resolve finds the node in m.
func Resolve(m *Model, n Node) (*Scope, error) {
	pi := slices.IndexFunc(m.Plants, func(p Plant) bool { return p.ID == n.Plant })
	if pi < 0 {
		return nil, fmt.Errorf("没有工厂 %s", n.Plant)
	}
	p := m.Plants[pi]
	s := &Scope{Node: n, Plant: pi, Line: -1, Step: -1, AreaIndex: -1, Lines: len(m.lineCfgs(pi))}
	base := "plants/" + itoa(pi)
	shared := func(ci int, what string) string {
		if ls := m.laid(pi, ci); len(ls) > 1 {
			return fmt.Sprintf("%s这 %d 条线是同一份设置，%s", m.lineTitle(pi, ci), len(ls), what)
		}
		return ""
	}
	switch n.Kind {
	case "plant":
		s.Title, s.Path = "工厂「"+p.Name+"」", base
	case "area":
		ai := slices.IndexFunc(p.Areas, func(a Area) bool { return a.Code == n.Area })
		if ai < 0 {
			return nil, fmt.Errorf("工厂「%s」没有编码为 %s 的车间", p.Name, n.Area)
		}
		a := p.Areas[ai]
		s.Title, s.AreaIndex = "车间「"+a.Name+"」", a.Index
		s.Path = base + "/hall"
		if a.Index >= 0 {
			s.Path = base + "/areas/" + itoa(a.Index)
		}
		for _, l := range p.Lines {
			if l.Area == a.Code && !slices.Contains(s.AreaLines, l.Cfg) {
				s.AreaLines = append(s.AreaLines, l.Cfg)
			}
		}
	case "line":
		li := slices.IndexFunc(p.Lines, func(l Line) bool { return l.ID == n.Line })
		if li < 0 {
			return nil, fmt.Errorf("工厂「%s」没有产线 %s", p.Name, n.Line)
		}
		s.Line = p.Lines[li].Cfg
		s.Title, s.Path, s.Shared = "产线"+m.lineTitle(pi, s.Line), base+"/lines/"+itoa(s.Line), shared(s.Line, "这里改了会一起变")
	case "machine":
		for _, l := range p.Lines {
			if si := slices.IndexFunc(l.Stations, func(st Station) bool { return st.ID == n.Asset }); si >= 0 {
				s.Line, s.Step = l.Cfg, si
				s.Title = fmt.Sprintf("设备 %s「%s」", n.Asset, l.Stations[si].Name)
				s.Path = fmt.Sprintf("%s/lines/%d/steps/%d", base, l.Cfg, si)
				s.Shared = shared(l.Cfg, "每条线上这个工位会一起变")
				break
			}
		}
		if s.Step < 0 {
			return nil, fmt.Errorf("工厂「%s」没有位号 %s", p.Name, n.Asset)
		}
	default:
		return nil, fmt.Errorf("要改的是 plant、area、line 或 machine，不是 %q", n.Kind)
	}
	return s, nil
}

// allows says whether a change at path p (split on /) of the config is inside the scope; prop
// is the proposed config (where a line moves to).
func (s *Scope) allows(p []string, prop map[string]any) bool {
	if len(p) < 3 || p[0] != "plants" || p[1] != itoa(s.Plant) || p[2] == "id" {
		return false
	}
	line := func(min int) (int, bool) {
		if len(p) < min || p[2] != "lines" {
			return 0, false
		}
		li, err := strconv.Atoi(p[3])
		return li, err == nil
	}
	switch s.Node.Kind {
	case "plant":
		return true
	case "line":
		li, ok := line(5)
		return ok && li == s.Line
	case "machine":
		li, ok := line(7)
		return ok && li == s.Line && p[4] == "steps" && p[5] == itoa(s.Step)
	case "area":
		if s.AreaIndex >= 0 && len(p) >= 5 && p[2] == "areas" && p[3] == itoa(s.AreaIndex) {
			return true
		}
		if s.AreaIndex < 0 && len(p) == 3 && p[2] == "hall" {
			return true
		}
		li, ok := line(5)
		if !ok || li >= s.Lines {
			return false
		}
		if slices.Contains(s.AreaLines, li) {
			return true
		}
		// a line of another area moved into this one
		if len(p) == 5 && p[4] == "area" && s.AreaIndex >= 0 {
			to := str(at(prop, "plants", s.Plant, "lines", li, "area"))
			name := str(at(prop, "plants", s.Plant, "areas", s.AreaIndex, "name"))
			return to != "" && strings.TrimSpace(to) == strings.TrimSpace(name)
		}
	}
	return false
}

// Outside are the changes from base to prop (configs of scope models) outside the scope, said in
// words, one per thing touched (产线「曲轴 2 线」工位「锻压」的节拍); none when all is inside.
func (s *Scope) Outside(base, prop *Model) []string {
	var out []string
	for _, p := range Diff(base.Config, prop.Config) {
		if s.allows(p, prop.Config) {
			continue
		}
		w := Where(base, prop, p)
		if !slices.Contains(out, w) {
			out = append(out, w)
		}
	}
	return out
}

// Diff is every path at which a and b differ: a key or an element only one side has, or a value
// that is not the same. $route (where a line's steps were copied from) is not a difference.
func Diff(a, b any) [][]string {
	var out [][]string
	var walk func(a, b any, p []string)
	walk = func(a, b any, p []string) {
		am, aok := a.(map[string]any)
		bm, bok := b.(map[string]any)
		if aok && bok {
			keys := map[string]bool{}
			for k := range am {
				keys[k] = true
			}
			for k := range bm {
				keys[k] = true
			}
			sorted := make([]string, 0, len(keys))
			for k := range keys {
				if k != "$route" {
					sorted = append(sorted, k)
				}
			}
			sort.Strings(sorted)
			for _, k := range sorted {
				av, ain := am[k]
				bv, bin := bm[k]
				if ain != bin {
					out = append(out, append(slices.Clone(p), k))
					continue
				}
				walk(av, bv, append(slices.Clone(p), k))
			}
			return
		}
		aa, aok := a.([]any)
		ba, bok := b.([]any)
		if aok && bok {
			for i := 0; i < max(len(aa), len(ba)); i++ {
				if i >= len(aa) || i >= len(ba) {
					out = append(out, append(slices.Clone(p), itoa(i)))
					continue
				}
				walk(aa[i], ba[i], append(slices.Clone(p), itoa(i)))
			}
			return
		}
		if !reflect.DeepEqual(a, b) {
			out = append(out, slices.Clone(p))
		}
	}
	walk(a, b, nil)
	return out
}

// field labels for the keys of a plant, an area, a line and a step
var fieldLabel = map[string]string{
	"name": "名称", "hall": "厂房", "badge": "徽标", "areas": "车间", "lines": "产线", "warehouse": "原料库", "finishedGoods": "成品库",
	"site": "场地", "sub": "副标题", "industry": "行业", "inventory": "库存", "carriers": "承运商", "freight": "运输", "demand": "客流",
	"kpis": "指标", "supply": "补货", "code": "编码", "note": "备注", "area": "所在车间", "product": "产品", "input": "来料状态",
	"route": "工艺路线", "steps": "工位", "repeat": "条数", "speed": "传送带速度", "beltHeight": "传送带高度", "order": "工单", "tables": "桌位",
	"machine": "设备种类", "cycle": "节拍", "makes": "产出状态", "measure": "测量", "power": "功率", "tooling": "模具", "take": "领用",
	"give": "归还", "thermal": "温度", "slots": "位数", "seats": "座位", "place": "位子", "until": "开放时间", "perPerson": "人均", "bySize": "按人数",
	"id": "编号", "short": "简称", "tariff": "电价", "ontology": "本体", "plants": "工厂",
}

// Where says in words what path p of the config is, by the names in base (or prop, for what
// only it has): 产线「曲轴 2 线」工位「锻压」的节拍.
func Where(base, prop *Model, p []string) string {
	if len(p) == 0 {
		return "整个公司"
	}
	if p[0] != "plants" {
		return "公司的" + label(p[0])
	}
	if len(p) < 2 {
		return "公司的工厂"
	}
	pi, _ := strconv.Atoi(p[1])
	m := base
	if pi >= len(base.Plants) {
		m = prop
	}
	if pi >= len(m.Plants) {
		return "新的工厂"
	}
	where := "工厂「" + m.Plants[pi].Name + "」"
	rest := p[2:]
	if len(rest) >= 2 && rest[0] == "areas" {
		ai, _ := strconv.Atoi(rest[1])
		name := str(at(base.Config, "plants", pi, "areas", ai, "name"))
		if name == "" {
			name = str(at(prop.Config, "plants", pi, "areas", ai, "name"))
		}
		where, rest = "车间「"+name+"」", rest[2:]
	} else if len(rest) >= 2 && rest[0] == "lines" {
		ci, _ := strconv.Atoi(rest[1])
		lm := base
		if ci >= len(base.lineCfgs(pi)) {
			lm = prop
		}
		where, rest = "产线"+lm.lineTitle(pi, ci), rest[2:]
		if len(rest) >= 2 && rest[0] == "steps" {
			si, _ := strconv.Atoi(rest[1])
			if st, ok := lm.station(pi, ci, si); ok {
				where += "工位「" + st.Name + "」"
			} else {
				where += "的第 " + itoa(si+1) + " 个工位"
			}
			rest = rest[2:]
		}
	}
	if len(rest) == 0 {
		return where
	}
	if rest[0] == "thermal" && len(rest) > 1 {
		return where + "的" + map[string]string{"warn": "报警温度", "trip": "停机温度"}[rest[1]]
	}
	return where + "的" + label(rest[0])
}

func label(k string) string {
	if l, ok := fieldLabel[k]; ok {
		return l
	}
	return k
}
