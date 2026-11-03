package knowledge

import (
	"slices"
	"strings"
	"testing"
)

// A part's BOM in effect joins the graph: the part uses each component, and is a part number of
// its product family; a question about what a crankshaft uses finds the BOM, and a BOM taken out
// of effect leaves with its components.
func TestBOMs(t *testing.T) {
	kb, ctx := base(t)
	boms := []BOMInfo{{Part: "QZ-B20-4", Name: "2.0T 四缸曲轴", Rev: "B", Drawing: "C", Effective: "2026-10-20", Family: "发动机曲轴", Lines: []BOMLineInfo{
		{Level: 1, Code: "QZ-B20-4-DJ", Label: "曲轴锻件", Kind: "自制件", Per: 1, Unit: "件", Op: "热模锻"},
		{Level: 2, Code: "GB-42CrMo-80", Label: "42CrMo 圆钢 φ80", Kind: "原材料", Per: 27.73, Unit: "kg", Op: "下料", Via: "曲轴锻件"},
		{Level: 1, Code: "BZ-PLUG-M10", Label: "油道螺塞 M10×1", Kind: "标准件", Per: 3, Unit: "件", Op: "装配"},
		{Level: 1, Code: "BZ-KEY", Label: "半圆键 5×7.5", Kind: "标准件", Per: 1, Unit: "件", Op: "装配"},
	}}}
	kb.BOMs = func(string) ([]BOMInfo, error) { return slices.Clone(boms), nil }
	if err := kb.SyncBOMs(ctx, "huaduan"); err != nil {
		t.Fatal(err)
	}
	st, _ := kb.Stats(ctx, "huaduan")
	if st.BOMs != 1 || st.Kinds[TComponent] != 4 || st.Relations[RUses] != 4 || st.Relations[RVariantOf] != 1 {
		t.Fatalf("BOMs in the graph: %+v", st)
	}
	e, _ := kb.Lookup(ctx, "huaduan", "2.0T 四缸曲轴")
	var uses []string
	family := ""
	for _, l := range e.Links {
		switch l.Type {
		case RUses:
			uses = append(uses, l.Name)
		case RVariantOf:
			family = l.Name
		}
	}
	if !slices.Contains(uses, "油道螺塞 M10×1") || len(uses) != 4 || family != "发动机曲轴" {
		t.Errorf("the crankshaft's links: %+v", e.Links)
	}
	if c, _ := kb.Lookup(ctx, "huaduan", "42CrMo 圆钢 φ80"); c == nil || c.Kind != TComponent || len(c.Links) != 1 || c.Links[0].Name != "2.0T 四缸曲轴" {
		t.Errorf("the bar: %+v", c)
	}
	a, err := kb.Search(ctx, "huaduan", "曲轴用到哪些标准件", nil, nil, 6)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, p := range a.Passages {
		if strings.Contains(p.Text, "用到的标准件：油道螺塞 M10×1、半圆键 5×7.5") {
			found = true
		}
	}
	if !found {
		t.Errorf("what the crankshaft uses: %+v", a.Passages)
	}

	boms = nil
	if err := kb.SyncBOMs(ctx, "huaduan"); err != nil {
		t.Fatal(err)
	}
	if st, _ := kb.Stats(ctx, "huaduan"); st.BOMs != 0 || st.Kinds[TComponent] != 0 {
		t.Errorf("after the BOM left: %+v", st)
	}
}
