package stores

import (
	"errors"
	"strings"
	"testing"

	"smartfactory/internal/engine"
)

// fakeTwin is a plant with one line forging 42CrMo crankshafts on a hammer press.
type fakeTwin struct {
	mats   []engine.PlantMaterial
	heats  []engine.Heat
	alerts map[string]string
}

func newTwin() *fakeTwin {
	p := engine.Press{ID: "HP-101", Name: "热模锻压力机", Cycles: 1000}
	p.Tool.Label, p.Tool.Life, p.Tool.Used = "热锻模", 10000, 500
	return &fakeTwin{alerts: map[string]string{}, mats: []engine.PlantMaterial{{ID: "p1", Name: "一厂", Lines: []engine.LineMaterial{{
		ID: "L1", Name: "曲轴 1 线", Product: "发动机曲轴", Material: "42CrMo", Input: 20,
		Presses: []engine.Press{p},
	}}}}}
}

func (t *fakeTwin) Materials(string) []engine.PlantMaterial { return t.mats }
func (t *fakeTwin) SetHeats(_ string, h []engine.Heat) bool { t.heats = h; return true }
func (t *fakeTwin) Alert(_, _, asset, code, text string) bool {
	t.alerts[asset+"/"+code] = text
	return true
}
func (t *fakeTwin) CompanyIDs() []string { return []string{"acme"} }
func (t *fakeTwin) MountTool(_, _, asset string, used, life int64) bool {
	for i := range t.mats[0].Lines[0].Presses {
		if p := &t.mats[0].Lines[0].Presses[i]; p.ID == asset {
			p.Tool.Used, p.Tool.Life = used, life
		}
	}
	return true
}

func open(t *testing.T) (*Service, *fakeTwin) {
	tw := newTwin()
	s, err := Open(t.TempDir(), tw, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	s.Categories = func(string) []Category {
		return []Category{{Key: "bar", Label: "棒料", Kind: Bar, Unit: "t", Fields: []string{"牌号", "规格"}}, {Key: "part", Label: "标准件", Kind: Part, Unit: "件"}}
	}
	return s, tw
}

func problems(err error) string {
	var p *Problems
	if errors.As(err, &p) {
		return p.Error()
	}
	return ""
}

func TestPartsLedgerAndSafetyStock(t *testing.T) {
	s, _ := open(t)
	var events []string
	s.Event = func(_, kind, _, text string) { events = append(events, kind+" "+text) }
	bolt, err := s.SaveItem("acme", Item{Cat: "part", Code: "M24", Name: "高强度螺栓", Safety: 20})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Record("acme", "王", MoveInput{Item: bolt.ID, Kind: In, Qty: 30}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Record("acme", "李", MoveInput{Item: bolt.ID, Kind: Out, Qty: 40}); !strings.Contains(problems(err), "只剩 30") {
		t.Fatalf("issued more than in store: %v", err)
	}
	_, after, err := s.Record("acme", "李", MoveInput{Item: bolt.ID, Kind: Out, Qty: 12, Ref: "HP-101 维修"})
	if err != nil || after.Stock != 18 || !after.Low {
		t.Fatalf("after issue: %+v %v", after, err)
	}
	if len(events) != 1 || !strings.Contains(events[0], "安全库存") {
		t.Fatalf("low stock not noted once: %v", events)
	}
	m, after, err := s.Record("acme", "王", MoveInput{Item: bolt.ID, Kind: Count, Qty: 16})
	if err != nil || m.Delta != -2 || after.Stock != 16 {
		t.Fatalf("count: %+v %+v %v", m, after, err)
	}
	if _, err := s.SaveItem("acme", Item{Cat: "part", Code: "M24", Name: "另一个"}); !strings.Contains(problems(err), "已经有了") {
		t.Fatalf("duplicate code: %v", err)
	}
}

func TestHeatsAreForgedByTheLines(t *testing.T) {
	s, tw := open(t)
	bar, _ := s.SaveItem("acme", Item{Cat: "bar", Code: "42CrMo-80", Name: "42CrMo 圆钢 φ80", Attrs: map[string]string{"牌号": "42CrMo", "规格": "φ80"}})
	if _, _, err := s.Record("acme", "王", MoveInput{Item: bar.ID, Kind: In, Qty: 10}); !strings.Contains(problems(err), "炉号") {
		t.Fatalf("bar without a heat: %v", err)
	}
	if _, _, err := s.Record("acme", "王", MoveInput{Item: bar.ID, Kind: In, Qty: 10, Heat: "26A10231"}); !strings.Contains(problems(err), "钢厂") {
		t.Fatalf("new heat without a mill: %v", err)
	}
	if _, _, err := s.Record("acme", "王", MoveInput{Item: bar.ID, Kind: In, Qty: 10, Heat: "26A10231", Mill: "华东特钢", Cert: "ZB-001"}); err != nil {
		t.Fatal(err)
	}
	s.Sync()
	if len(tw.heats) != 1 || tw.heats[0].Kg != 10000 || tw.heats[0].Material != "42CrMo" {
		t.Fatalf("heats given to the twin: %+v", tw.heats)
	}
	// the line takes the heat and forges 50 pieces of 20 kg for MO-1
	l := &tw.mats[0].Lines[0]
	l.Lot, l.Job = &engine.LotNow{No: "26A10231", Supplier: "华东特钢", Left: 400}, &engine.JobNow{No: "MO-1", Heat: "26A10231"}
	s.Sync()
	if tw.heats[0].Kg != 10000-400*20 {
		t.Fatalf("the lot taken is not held back: %+v", tw.heats)
	}
	l.Lot.Left = 350
	s.Sync()
	hs, _ := s.Heats("acme", "26A10231")
	if hs[0].Used != 1 || hs[0].Left != 9 {
		t.Fatalf("heat after 50 pieces: %+v", hs[0])
	}
	it, _ := s.Item("acme", bar.ID)
	if it.Stock != 9 || len(it.Heats) != 1 {
		t.Fatalf("bar stock: %+v", it)
	}
	tr, err := s.TraceOf("acme", "MO-1")
	if err != nil || len(tr.Uses) != 1 || tr.Uses[0].Heat != "26A10231" || tr.Uses[0].Pieces != 50 || !tr.Uses[0].Stored {
		t.Fatalf("order trace: %+v %v", tr, err)
	}
	tr, err = s.TraceOf("acme", "26A10231")
	if err != nil || tr.Heat == nil || tr.Heat.Cert != "ZB-001" || len(tr.Moves) != 1 || tr.Uses[0].Order != "MO-1" {
		t.Fatalf("heat trace: %+v %v", tr, err)
	}
	// a heat the twin made up traces with a note
	l.Lot, l.Job = &engine.LotNow{No: "26Z99999", Supplier: "北方重钢", Left: 100}, &engine.JobNow{No: "MO-2"}
	s.Sync()
	l.Lot.Left = 90
	s.Sync()
	tr, _ = s.TraceOf("acme", "MO-2")
	if tr == nil || tr.Note == "" {
		t.Fatalf("unbooked heat: %+v", tr)
	}
}

func TestDieStrokesAndChanges(t *testing.T) {
	s, tw := open(t)
	if _, err := s.SaveDie("acme", "孙", Die{No: "M-1", Kind: "辊锻模", Life: 1000}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Act("acme", "孙", "M-1", DieAction{Do: "mount", Plant: "p1", Asset: "HP-101"}); !strings.Contains(problems(err), "热锻模") {
		t.Fatalf("a die of another kind went on: %v", err)
	}
	if _, err := s.SaveDie("acme", "孙", Die{No: "M-2", Kind: "热锻模", Life: 1000, Strokes: 850}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SaveDie("acme", "孙", Die{No: "M-3", Kind: "热锻模", Product: "曲轴", Life: 1000}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Act("acme", "孙", "M-2", DieAction{Do: "mount", Plant: "p1", Asset: "HP-101"}); err != nil {
		t.Fatal(err)
	}
	press := &tw.mats[0].Lines[0].Presses[0]
	s.Sync()
	press.Cycles += 60
	press.Tool.Used += 60
	s.Sync()
	d, _ := s.Die("acme", "M-2")
	if d.Strokes != 910 || d.Warned != 0.9 || !strings.Contains(tw.alerts["HP-101/die-life"], "M-2") {
		t.Fatalf("strokes and warning: %+v %v", d, tw.alerts)
	}
	// the twin changes its tool by itself: M-2 off for re-sinking, the spare for this part on
	press.Cycles += 10
	press.Tool.Used = 0
	s.Sync()
	d, _ = s.Die("acme", "M-2")
	on, _ := s.DieOn("acme", "p1", "HP-101")
	if d.Place != Repair || on == nil || on.No != "M-3" {
		t.Fatalf("after the twin's change: %+v on %+v", d, on)
	}
	if _, err := s.Act("acme", "孙", "M-2", DieAction{Do: "repair", Resunk: true, Life: 800}); err != nil {
		t.Fatal(err)
	}
	d, _ = s.Die("acme", "M-2")
	if d.Place != InStore || d.Resinks != 1 || d.Life != 800 || d.Warned != 0 {
		t.Fatalf("re-sunk: %+v", d)
	}
	h, _ := s.DieHistory("acme", "M-2", 0)
	if len(h) < 5 || h[0].Kind != "repair" {
		t.Fatalf("history: %+v", h)
	}
}

func TestProjectReleasesItsTrialDie(t *testing.T) {
	s, _ := open(t)
	p, err := s.SaveProject("acme", "陈", Project{Part: "ZXJ-1", Name: "转向节"})
	if err != nil || p.Stage != "立项" {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := s.SaveDie("acme", "陈", Die{No: "T-1", Kind: "热锻模", Life: 5000, Place: InTrial, Project: p.ID}, true); err != nil {
		t.Fatal(err)
	}
	p, err = s.AddTrial("acme", "周", Trial{Project: "ZXJ-1", Die: "T-1", Qty: 10, Good: 6, Result: "需改模", Findings: "R 角充不满"})
	if err != nil || p.Stage != "试锻" || len(p.Trials) != 1 || len(p.Dies) != 1 {
		t.Fatalf("after a trial: %+v %v", p, err)
	}
	if _, err := s.Release("acme", "厂长", p.ID, SignOff{}); !strings.Contains(problems(err), "合格") {
		t.Fatalf("released without a good trial: %v", err)
	}
	s.AddTrial("acme", "周", Trial{Project: p.ID, Die: "T-1", Qty: 20, Good: 20, Result: "合格"})
	p, err = s.Release("acme", "厂长", p.ID, SignOff{})
	if err != nil || p.Stage != "量产" || p.Released == nil || p.Dies[0].Place != InStore {
		t.Fatalf("released: %+v %v", p, err)
	}
}

func TestSeedOnlyTheDemo(t *testing.T) {
	s, tw := open(t)
	tw.mats[0].Lines[0].Lot = &engine.LotNow{No: "x", Supplier: "华东特钢", Left: 5}
	if err := s.Seed("acme"); err != nil {
		t.Fatal(err)
	}
	if its, _ := s.Items("acme", ""); len(its) != 0 {
		t.Fatalf("a company of its own got the demo's stores: %d items", len(its))
	}
	if err := s.Seed(Demo); err != nil {
		t.Fatal(err)
	}
	its, _ := s.Items(Demo, "")
	low := 0
	for _, it := range its {
		if it.Low {
			low++
		}
	}
	ds, _ := s.Dies(Demo)
	ps, _ := s.Projects(Demo)
	if len(its) < 5 || low == 0 || len(ds) < 3 || len(ps) != 1 || len(ps[0].Trials) != 2 {
		t.Fatalf("demo: %d items (%d low), %d dies, %+v", len(its), low, len(ds), ps)
	}
	if err := s.Seed(Demo); err != nil {
		t.Fatal(err)
	}
	if again, _ := s.Items(Demo, ""); len(again) != len(its) {
		t.Fatal("seeded twice")
	}
}
