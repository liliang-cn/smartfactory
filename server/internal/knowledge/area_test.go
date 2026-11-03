package knowledge

import (
	"strings"
	"testing"
)

func areaPlant() Plant {
	st := func(id, name string) Station { return Station{ID: id, Name: name, Label: name, Cycle: 60} }
	return Plant{
		ID: "p1", Name: "一厂", Hall: "生产车间", Site: "hall", Industry: "压铸",
		Areas: []Area{
			{Code: "MLT", Name: "熔化车间", Lines: []string{"L1"}},
			{Code: "DC", Name: "压铸车间", Lines: []string{"L2", "L3"}},
			{Code: "FG", Name: "成品库"},
		},
		Lines: []Line{
			{ID: "L1", Name: "熔化线", Area: "MLT", Product: Product{Key: "al", Name: "铝液"}, Stations: []Station{st("MF-101", "熔化炉")}},
			{ID: "L2", Name: "压铸 1 线", Area: "DC", Product: Product{Key: "hs", Name: "壳体"}, Stations: []Station{st("DC-201", "压铸机"), st("TR-201", "切边机")}},
			{ID: "L3", Name: "压铸 2 线", Area: "DC", Product: Product{Key: "hs", Name: "壳体"}, Stations: []Station{st("DC-301", "压铸机")}},
		},
	}
}

func itemByID(items []item, id string) *item {
	for i := range items {
		if items[i].id == id {
			return &items[i]
		}
	}
	return nil
}

func hasRel(it *item, from, typ, to string) bool {
	for _, r := range it.rels {
		if r.From == from && r.Type == typ && r.To == to {
			return true
		}
	}
	return false
}

// The area level: plant has_area area has_line line, one passage per area naming its machines by
// tag (so "压铸车间有哪些设备" finds it), and a line's and station's passages say which area.
func TestAreaItems(t *testing.T) {
	m := &Model{ID: "dc", Name: "宏远压铸", Plants: []Plant{areaPlant()}}
	items := processItems(m)

	dc := itemByID(items, "proc:area:p1:DC")
	if dc == nil {
		t.Fatal("no passage for 压铸车间")
	}
	for _, w := range []string{"压铸车间", "一厂", "2 条线", "3 台设备", "DC-201 压铸机", "TR-201", "DC-301"} {
		if !strings.Contains(dc.text, w) {
			t.Errorf("压铸车间 passage lacks %q: %s", w, dc.text)
		}
	}
	if strings.Contains(dc.text, "MF-101") {
		t.Errorf("压铸车间 names the melting furnace: %s", dc.text)
	}
	if !hasRel(dc, "一厂", RHasArea, "压铸车间") || !hasRel(dc, "压铸车间", RHasLine, "压铸 1 线") || !hasRel(dc, "压铸车间", RHasLine, "压铸 2 线") {
		t.Errorf("压铸车间 relations: %+v", dc.rels)
	}
	if fg := itemByID(items, "proc:area:p1:FG"); fg == nil || !strings.Contains(fg.text, "没有产线") {
		t.Errorf("an empty area: %+v", fg)
	}
	pl := itemByID(items, "proc:plant:p1")
	if !strings.Contains(pl.text, "分 3 个车间") || !hasRel(pl, "一厂", RHasArea, "熔化车间") {
		t.Errorf("plant passage: %s %+v", pl.text, pl.rels)
	}
	if l := itemByID(items, "proc:line:p1:L2"); !strings.Contains(l.text, "在压铸车间") || l.meta["area"] != "DC" || !hasRel(l, "压铸车间", RHasLine, "压铸 1 线") {
		t.Errorf("line passage: %s %v", l.text, l.meta)
	}
	if s := itemByID(items, "proc:station:p1:TR-201"); !strings.Contains(s.text, "压铸车间") || s.meta["area"] != "DC" {
		t.Errorf("station passage: %s %v", s.text, s.meta)
	}
}

// A model without areas (before plants had them) has one, its hall, with every line; the same
// hall in two plants is told apart by the plant's name.
func TestAreaDefaultItems(t *testing.T) {
	a, b := areaPlant(), areaPlant()
	a.Areas, b.Areas = nil, nil
	b.ID, b.Name = "p2", "二厂"
	m := &Model{ID: "dc", Name: "宏远压铸", Plants: []Plant{a, b}}
	if as := a.areas(); len(as) != 1 || as[0].Name != "生产车间" || len(as[0].Lines) != 3 || a.areaOf("L3").Code != "A1" {
		t.Fatalf("default areas: %+v", as)
	}
	items := processItems(m)
	it := itemByID(items, "proc:area:p2:A1")
	if it == nil || !hasRel(it, "二厂", RHasArea, "二厂 生产车间") || !strings.Contains(it.text, "3 条线") {
		t.Fatalf("default area passage: %+v", it)
	}
	if pl := itemByID(items, "proc:plant:p1"); !strings.Contains(pl.text, "车间「生产车间」") {
		t.Errorf("a one-area plant reads as before: %s", pl.text)
	}

	// the ontology: 工厂 有车间 车间, 产线 所在车间 车间
	p := compileOntology(&Model{ID: "dc", Name: "宏远压铸", Plants: []Plant{areaPlant()}})
	var has, in bool
	for _, tr := range p.triples {
		has = has || tr.Predicate.Value == relIRI("有车间").Value && tr.Object.Value == thingIRI("压铸车间").Value
		in = in || tr.Subject.Value == thingIRI("压铸 2 线").Value && tr.Predicate.Value == relIRI("所在车间").Value && tr.Object.Value == thingIRI("压铸车间").Value
	}
	if !has || !in || p.things["压铸车间"] != TArea {
		t.Errorf("ontology: 有车间 %v 所在车间 %v kind %q", has, in, p.things["压铸车间"])
	}
}
