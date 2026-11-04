package scoped

import (
	"fmt"
	"slices"
	"strings"
)

// Split makes one copy of a repeated line (a line entry with repeat > 1) an entry of its own, so
// a change to it leaves the other copies as they are. The node is the line, or a machine on it,
// of that copy. The copies stay in the same order, so every line number, and with it every
// machine tag (位号), is as before; names are written out for the copies that no longer follow
// the entry's {n} pattern. ok is false when the node's line is not repeated; msg says what was
// done, in plain Chinese. The returned config is a copy of m's.
func Split(m *Model, n Node) (cfg map[string]any, msg string, ok bool) {
	pi := slices.IndexFunc(m.Plants, func(p Plant) bool { return p.ID == n.Plant })
	if pi < 0 {
		return nil, "", false
	}
	var line *Line
	for i, l := range m.Plants[pi].Lines {
		hit := n.Kind == "line" && l.ID == n.Line
		if n.Kind == "machine" {
			hit = slices.ContainsFunc(l.Stations, func(st Station) bool { return st.ID == n.Asset })
		}
		if hit {
			line = &m.Plants[pi].Lines[i]
			break
		}
	}
	if line == nil {
		return nil, "", false
	}
	copies := m.laid(pi, line.Cfg)
	if len(copies) < 2 {
		return nil, "", false
	}
	cfg = obj(clone(m.Config))
	plant := obj(at(cfg, "plants", pi))
	entries := arr(plant["lines"])
	entry := obj(entries[line.Cfg])
	tmpl := str(entry["name"])
	// the entry's name for its i-th copy, as the layout writes it
	literal := func(i int) string {
		if strings.Contains(tmpl, "{n}") {
			return strings.ReplaceAll(tmpl, "{n}", itoa(i))
		}
		return fmt.Sprintf("%s %d", tmpl, i)
	}
	one := func(i int) map[string]any {
		e := obj(clone(entry))
		e["repeat"] = float64(1)
		e["name"] = literal(i)
		return e
	}
	var out []any
	out = append(out, entries[:line.Cfg]...)
	if line.Rep > 2 { // two or more copies before it keep their entry (and its pattern)
		head := obj(clone(entry))
		head["repeat"] = float64(line.Rep - 1)
		out = append(out, head)
	} else if line.Rep == 2 {
		out = append(out, one(1))
	}
	out = append(out, one(line.Rep))
	for i := line.Rep + 1; i <= len(copies); i++ {
		out = append(out, one(i))
	}
	out = append(out, entries[line.Cfg+1:]...)
	plant["lines"] = out

	var others []string
	for _, c := range copies {
		if c.ID != line.ID {
			others = append(others, "「"+c.Name+"」")
		}
	}
	what := "这条线"
	if n.Kind == "machine" {
		what = "这台设备所在的产线"
	}
	msg = fmt.Sprintf("产线「%s」原来和另外 %d 条线（%s）是同一份设置。这次只改它自己：%s会单独拆成一项，另外 %d 条保持原样；产线编号和所有位号都不变。",
		line.Name, len(copies)-1, strings.Join(others, "、"), what, len(copies)-1)
	return cfg, msg, true
}
