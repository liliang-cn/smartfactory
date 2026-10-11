package knowledge

import (
	"slices"
	"strings"
	"testing"
)

func testModel() *Model {
	return &Model{ID: "jh", Name: "江河锻造", Plants: []Plant{{
		ID: "p1", Name: "一厂",
		Lines: []Line{{
			ID: "L1", Name: "曲轴 1 线",
			Product: Product{Key: "crank", Name: "发动机曲轴", PartNo: "QZ-B20-4", Variants: []string{"QZ-B15-4", "QZ-B20-4"}, Suppliers: []string{"宝"}, Customers: []string{"远岳动力"}},
			Stations: []Station{
				{ID: "IH-101", Label: "中频感应加热炉", Code: "IH"},
				{ID: "HP-101", Label: "热模锻压力机", Code: "HP"},
			},
		}},
	}}}
}

func linked(text string) []string {
	var out []string
	for _, t := range link(terms(testModel()), text) {
		out = append(out, t.typ+":"+t.name)
	}
	slices.Sort(out)
	return out
}

// Names are found the way people write them, identifiers only as whole words, a machine code
// only with a number or series after it, and nothing by a single character.
func TestLink(t *testing.T) {
	cases := []struct {
		text string
		want []string
		not  []string
	}{
		{"ih101 出炉后 8 秒内上锤", []string{"station:IH-101", "machine:中频感应加热炉"}, nil},
		{"设备=IH 101；功率=800 kW", []string{"station:IH-101"}, nil},
		{"HP-1012 是二期的压力机", []string{"machine:热模锻压力机"}, []string{"station:HP-101"}},
		{"HP 系列压力机每 5000 次换模", []string{"machine:热模锻压力机"}, []string{"station:HP-101"}},
		{"HPLC 分析结果", nil, []string{"machine:热模锻压力机"}},
		{"料号 qz-b15-4 的切边", []string{"product:发动机曲轴"}, nil},
		{"料号 QZ-B15-40 是新品", nil, []string{"product:发动机曲轴"}},
		{"在曲轴1线完成", []string{"line:曲轴 1 线"}, nil},
		{"宝钢的坯料", nil, []string{"supplier:宝"}},
		{"交付远岳动力", []string{"customer:远岳动力"}, nil},
	}
	for _, c := range cases {
		got := linked(c.text)
		for _, w := range c.want {
			if !slices.Contains(got, w) {
				t.Errorf("%q: no %s in %v", c.text, w, got)
			}
		}
		for _, w := range c.not {
			if slices.Contains(got, w) {
				t.Errorf("%q: should not link %s (%v)", c.text, w, got)
			}
		}
		if c.want == nil && c.not != nil && len(got) > 0 && strings.Contains(c.text, "HPLC") {
			t.Errorf("%q linked %v", c.text, got)
		}
	}
}
