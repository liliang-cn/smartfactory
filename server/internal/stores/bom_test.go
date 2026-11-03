package stores

import (
	"math"
	"strings"
	"testing"
)

func near(a, b float64) bool { return math.Abs(a-b) < 1e-6 }

// crank is a crankshaft over its forging over 42CrMo bar, with a timing gear that has a BOM of
// its own (40Cr bar), and three plugs.
func crank(s *Service, t *testing.T) {
	t.Helper()
	gear, err := s.SaveBOM("acme", "陈工", BOM{Part: "G28", Name: "正时齿轮", Lines: []BOMLine{
		{Code: "GB-40Cr-90", Name: "40Cr 圆钢", Kind: Raw, Grade: "40Cr", Spec: "φ90", Qty: 1.25, Unit: "kg", Scrap: 4},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ReleaseBOM("acme", "总工", gear.ID, Release{}); err != nil {
		t.Fatal(err)
	}
	b, err := s.SaveBOM("acme", "陈工", BOM{Part: "QZ-1", Name: "曲轴", Drawing: "B", Lines: []BOMLine{
		{ID: "F", Code: "QZ-1-DJ", Name: "曲轴锻件", Kind: Make, Qty: 1, Op: "热模锻"},
		{Parent: "F", Code: "BAR42", Name: "圆钢", Grade: "42CrMo", Spec: "φ80", Kind: Raw, Qty: 27.4, Unit: "kg", Scrap: 2, Op: "下料"},
		{Code: "G28", Name: "正时齿轮", Kind: Make, Qty: 1, Op: "装配"},
		{Code: "PLUG", Name: "油道螺塞", Kind: Std, Spec: "M10×1", Qty: 2, Op: "装配"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if b.Rev != "A" || b.Status != Draft || b.Lines[1].Parent != "F" {
		t.Fatalf("draft: %+v", b)
	}
	if _, _, err := s.ReleaseBOM("acme", "总工", b.ID, Release{}); err != nil {
		t.Fatal(err)
	}
}

// Revisions: a draft is checked and changed, goes in effect with a change record, is copied to
// the next revision, and that one retires it; one revision is in effect at a time.
func TestBOMRevisions(t *testing.T) {
	s, _ := open(t)
	var changed int
	s.BOMChanged = func(string) { changed++ }
	_, err := s.SaveBOM("acme", "陈工", BOM{Part: "QZ-1", Name: "曲轴", Lines: []BOMLine{{Name: "螺塞", Qty: 0}, {Name: "键", Qty: 1, Parent: "nope"}}})
	if p := problems(err); !strings.Contains(p, "用量要大于 0") || !strings.Contains(p, "上级不在") {
		t.Fatalf("bad lines: %v", err)
	}
	if _, err := s.SaveBOM("acme", "陈工", BOM{Part: "QZ-1", Name: "曲轴", Lines: []BOMLine{{Code: "QZ-1", Qty: 1}}}); !strings.Contains(problems(err), "这个零件自己") {
		t.Fatalf("a part in its own BOM: %v", err)
	}
	crank(s, t)
	a, _ := s.BOMOf("acme", "QZ-1")
	if a.Status != Active || a.Effective == "" || a.ReleasedBy != "总工" {
		t.Fatalf("in effect: %+v", a)
	}
	if _, err := s.SaveBOM("acme", "陈工", *a); !strings.Contains(problems(err), "出新版本") {
		t.Fatalf("an effective revision changed in place: %v", err)
	}
	b, err := s.CopyBOM("acme", "陈工", a.ID)
	if err != nil || b.Rev != "B" || b.Status != Draft || len(b.Lines) != 4 {
		t.Fatalf("copy: %+v %v", b, err)
	}
	b.Lines[3].Qty = 3
	b.Lines = append(b.Lines, BOMLine{Code: "PIN", Name: "定位销", Kind: Std, Qty: 2})
	b.Lines = append(b.Lines[:2], b.Lines[3:]...) // the gear goes
	if b, err = s.SaveBOM("acme", "陈工", *b); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ReleaseBOM("acme", "总工", b.ID, Release{}); !strings.Contains(problems(err), "为什么改") {
		t.Fatalf("a change without a reason: %v", err)
	}
	_, e, err := s.ReleaseBOM("acme", "总工", b.ID, Release{Why: "客户图纸 C 版加油道", Drawing: "C"})
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(e.Changes, "；")
	for _, want := range []string{"图纸 B → C 版", "油道螺塞 M10×1 用量 2 件 → 3 件", "加 定位销 2 件", "去掉 正时齿轮 1 件"} {
		if !strings.Contains(all, want) {
			t.Errorf("ECO lacks %q: %s", want, all)
		}
	}
	if e.From != "A" || e.To != "B" || e.By != "总工" {
		t.Errorf("ECO: %+v", e)
	}
	revs, _ := s.BOMs("acme", "QZ-1")
	if len(revs) != 2 || revs[0].Status != Retired || revs[1].Status != Active {
		t.Fatalf("after B: %+v", revs)
	}
	ecos, _ := s.ECOs("acme", "qz-1", 0)
	if len(ecos) != 2 || ecos[0].To != "B" {
		t.Fatalf("ECOs: %+v", ecos)
	}
	if _, err := s.DeleteBOM("acme", revs[1].ID); !strings.Contains(problems(err), "只能删草稿") {
		t.Fatalf("deleted an effective revision: %v", err)
	}
	if _, err := s.RetireBOM("acme", "总工", revs[1].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Explode("acme", "QZ-1", "", 1); err != ErrNotFound {
		t.Fatalf("exploded a part with none in effect: %v", err)
	}
	if changed < 6 {
		t.Errorf("BOMChanged heard %d changes", changed)
	}
	parts, _ := s.Parts("acme")
	if len(parts) != 2 || parts[1].Part != "QZ-1" || parts[1].Active != "" || parts[1].Revisions != 2 {
		t.Errorf("parts: %+v", parts)
	}
}

// Exploding goes down the tree and into a sub-assembly's own BOM, adds scrap, and sets what is
// needed against the stores: bar in t against kg, an item kept by grade and size, one not kept.
func TestExplodeAndKit(t *testing.T) {
	s, _ := open(t)
	crank(s, t)
	bar, err := s.SaveItem("acme", Item{Cat: "bar", Code: "GB-42CrMo-80", Name: "42CrMo 圆钢 φ80", Attrs: map[string]string{"牌号": "42CrMo", "规格": "Φ80"}, Unit: "t"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Record("acme", "王", MoveInput{Item: bar.ID, Kind: In, Qty: 4, Heat: "H1", Mill: "华东特钢"}); err != nil {
		t.Fatal(err)
	}
	plug, _ := s.SaveItem("acme", Item{Cat: "part", Code: "PLUG", Name: "油道螺塞", Unit: "件"})
	s.Record("acme", "王", MoveInput{Item: plug.ID, Kind: In, Qty: 250})

	x, err := s.Explode("acme", "QZ-1", "", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(x.Rows) != 5 || x.Rows[2].Sub != "G28" || x.Rows[3].Level != 2 || !x.Rows[3].Leaf || x.Rows[0].Leaf {
		t.Fatalf("rows: %+v", x.Rows)
	}
	need := map[string]Need{}
	for _, n := range x.Needs {
		need[n.Code] = n
	}
	b := need["BAR42"]
	if !near(b.Need, 2794.8) || b.Item != bar.ID || !near(b.Stock, 4000) || b.StoreUnit != "t" || b.Short != 0 {
		t.Errorf("bar by grade and size, kg against t: %+v", b)
	}
	if g := need["GB-40Cr-90"]; !near(g.Need, 130) || g.Problem != "库房没有建档" || !near(g.Short, 130) {
		t.Errorf("the gear's bar, not kept: %+v", g)
	}
	if p := need["PLUG"]; p.Need != 200 || p.Short != 0 || p.Have != 200 {
		t.Errorf("plugs: %+v", p)
	}
	if x.Short != 1 || x.Unkept != 1 {
		t.Errorf("short %d unkept %d", x.Short, x.Unkept)
	}

	// two orders: the first takes its plugs, the second is short of them and of bar
	k, err := s.Kit("acme", []Demand{{No: "MO-1", Part: "QZ-1", Qty: 100}, {No: "MO-2", Part: "QZ-1", Qty: 50}, {No: "MO-3", Part: "LG-9", Qty: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if k.Orders[0].Ready || k.Orders[0].Short != 1 || k.Orders[2].Rev != "" {
		t.Fatalf("orders: %+v", k.Orders)
	}
	second := map[string]Need{}
	for _, n := range k.Orders[1].Needs {
		second[n.Code] = n
	}
	if p := second["PLUG"]; p.Have != 50 || p.Short != 50 {
		t.Errorf("the second order's plugs: %+v", p)
	}
	if b := second["BAR42"]; !near(b.Have, 4000-2794.8) || !near(b.Short, 1397.4-(4000-2794.8)) {
		t.Errorf("the second order's bar: %+v", b)
	}
	var plugShort *Shortage
	for i, sh := range k.Short {
		if sh.Code == "PLUG" {
			plugShort = &k.Short[i]
		}
	}
	if plugShort == nil || plugShort.Short != 50 || plugShort.Need.Need != 300 || len(plugShort.Orders) != 1 || plugShort.Orders[0] != "MO-2" {
		t.Errorf("plug shortage: %+v", plugShort)
	}

	// where-used walks into the gear's own BOM
	us, _ := s.WhereUsed("acme", "40Cr 棒料")
	if len(us) != 2 || us[0].Part != "G28" || us[1].Part != "QZ-1" || us[1].Via != "正时齿轮" || !near(us[1].Per, 1.3) {
		t.Errorf("where 40Cr bar goes: %+v", us)
	}
	if us, _ := s.WhereUsed("acme", "标准件"); len(us) != 1 || us[0].Code != "PLUG" {
		t.Errorf("the standard parts: %+v", us)
	}
}

// A sheet in the shapes factories keep: levels as numbers or dots, a parent-code column, scrap as
// 2% or 0.02, a header in its own words below a title row.
func TestImportBOM(t *testing.T) {
	s, _ := open(t)
	rows := [][]string{
		{"曲轴 BOM", "", ""},
		{"层级", "物料编码", "物料名称", "规格型号", "材质", "单件用量", "单位", "损耗率", "工序", "物料类型"},
		{"0", "QZ-9", "试制曲轴", "", "", "1", "件", "", "", ""},
		{"1", "QZ-9-DJ", "曲轴锻件", "", "", "1", "件", "", "热模锻", "自制件"},
		{"..2", "GB-42", "圆钢", "φ80", "42CrMo", "27.4", "kg", "2%", "下料", ""},
		{"1", "M10", "油道螺塞", "GB/T 3", "", "3", "个", "", "装配", ""},
		{"1", "S330", "钢丸", "", "", "0.12", "kg", "0.05", "抛丸", ""},
		{"1", "X", "坏行", "", "", "abc", "", "", "", ""},
	}
	b, im, err := s.ImportBOM("acme", "陈工", BOM{}, rows)
	if err != nil {
		t.Fatal(err)
	}
	if b.Part != "QZ-9" || b.Name != "试制曲轴" || b.Source != "导入" || len(b.Lines) != 4 {
		t.Fatalf("imported: %+v", b)
	}
	l := b.Lines
	if l[1].Parent != l[0].ID || l[1].Kind != Raw || l[1].Scrap != 2 || l[1].Grade != "42CrMo" || l[2].Kind != Std || l[2].Parent != "" || l[3].Scrap != 5 || l[3].Kind != Aux {
		t.Errorf("lines: %+v", l)
	}
	if len(im.Problems) != 1 || !strings.Contains(im.Problems[0], "第 8 行") {
		t.Errorf("problems: %v", im.Problems)
	}

	// the file names one part and the form another: refused, not filed under the wrong part
	if _, _, err := s.ImportBOM("acme", "陈工", BOM{Part: "QZ-8"}, rows); !strings.Contains(problems(err), "表里写的零件是 QZ-9") {
		t.Errorf("part mismatch: %v", err)
	}

	// by parent code, the part named
	rows = [][]string{
		{"上级编码", "子件编码", "名称", "数量", "单位"},
		{"LG-1", "LG-1-DJ", "连杆锻件", "1", "件"},
		{"LG-1-DJ", "C70", "C70S6 圆钢", "0.74", "kg"},
		{"LG-1", "BOLT", "连杆螺栓", "2", "件"},
	}
	b, _, err = s.ImportBOM("acme", "陈工", BOM{Part: "LG-1", Name: "连杆", Rev: "a"}, rows)
	if err != nil {
		t.Fatal(err)
	}
	if b.Rev != "A" || len(b.Lines) != 3 || b.Lines[1].Parent != b.Lines[0].ID || b.Lines[2].Parent != "" || b.Lines[2].Kind != Std {
		t.Errorf("by parent: %+v", b.Lines)
	}
	if _, _, err := s.ImportBOM("acme", "陈工", BOM{Part: "X"}, [][]string{{"a", "b"}, {"1", "2"}}); !strings.Contains(problems(err), "找不到表头") {
		t.Errorf("no header: %v", err)
	}
}

// The demo company's BOMs: every part number its lines make, the 2.0T crankshaft in its second
// revision with the gear's own BOM under it, and a draft for the part in development; once.
func TestSeedBOM(t *testing.T) {
	s, _ := open(t)
	s.Products = func(string) []ProductRef {
		return []ProductRef{
			{Part: "QZ-B15-4", Name: "1.5L 四缸曲轴", Product: "crankshaft", Family: "发动机曲轴", Material: "42CrMo", Weight: 17.8, Input: 22.6},
			{Part: "QZ-B20-4", Name: "2.0T 四缸曲轴", Product: "crankshaft", Family: "发动机曲轴", Material: "42CrMo", Weight: 21.6, Input: 27.4},
		}
	}
	if err := s.SeedBOM(Demo); err != nil {
		t.Fatal(err)
	}
	if bs, _ := s.BOMs(Demo, ""); len(bs) != 0 {
		t.Fatal("seeded BOMs before the stores")
	}
	if err := s.Seed(Demo); err != nil {
		t.Fatal(err)
	}
	if err := s.SeedBOM(Demo); err != nil {
		t.Fatal(err)
	}
	if err := s.SeedBOM(Demo); err != nil {
		t.Fatal(err)
	}
	parts, _ := s.Parts(Demo)
	got := map[string]PartRow{}
	for _, p := range parts {
		got[p.Part] = p
	}
	if got["QZ-B20-4"].Active != "B" || got["QZ-B15-4"].Active != "A" || got["ZSCL-28T"].Active != "A" || got["ZXJ-2201"].Drafts != 1 || len(parts) != 4 {
		t.Fatalf("parts: %+v", parts)
	}
	x, err := s.Explode(Demo, "QZ-B20-4", "", 1000)
	if err != nil {
		t.Fatal(err)
	}
	need := map[string]Need{}
	for _, n := range x.Needs {
		need[n.Code] = n
	}
	if g := need["GB-40Cr-90"]; g.Item == "" || g.Short <= 0 || need["BZ-PLUG-M10"].Need != 3000 || need["BZ-PLUG-M10"].Short != 400 {
		t.Errorf("needs for 1000: %+v", x.Needs)
	}
	if ecos, _ := s.ECOs(Demo, "QZ-B20-4", 0); len(ecos) != 2 || !strings.Contains(strings.Join(ecos[0].Changes, "；"), "油道螺塞") {
		t.Errorf("ECOs: %+v", ecos)
	}
}

// Signing a new part off puts its BOM in effect with a change record that points back at the
// project and the drawing it was released to.
func TestSignOffReleasesBOM(t *testing.T) {
	s, _ := open(t)
	p, err := s.SaveProject("acme", "陈工", Project{Part: "ZXJ-1", Name: "转向节", Grade: "42CrMo", Weight: 6.8, Drawing: "B"})
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.DraftBOM("acme", "陈工", p.ID)
	if err != nil || d.Project != p.ID || d.Source != "研发" || d.Lines[0].Grade != "42CrMo" || d.Lines[0].Qty != 6.8 {
		t.Fatalf("draft from the project: %+v %v", d, err)
	}
	if _, err := s.AddTrial("acme", "周", Trial{Project: p.ID, Qty: 10, Good: 10, Result: "合格"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Release("acme", "厂长", p.ID, SignOff{BOM: "nope"}); !strings.Contains(problems(err), "没有这个 BOM") {
		t.Fatalf("a BOM that is not there: %v", err)
	}
	p, err = s.Release("acme", "厂长", p.ID, SignOff{BOM: d.ID, Drawing: "C"})
	if err != nil || p.Stage != "量产" || p.Drawing != "C" {
		t.Fatalf("signed off: %+v %v", p, err)
	}
	b, _ := s.BOMOf("acme", "ZXJ-1")
	ecos, _ := s.ECOs("acme", "ZXJ-1", 0)
	if b.Status != Active || b.Drawing != "C" || len(ecos) != 1 || ecos[0].Project != p.ID || !strings.Contains(ecos[0].Why, "批准量产") || ecos[0].By != "厂长" {
		t.Errorf("BOM %+v ECOs %+v", b, ecos)
	}
}
