package edge

import (
	"strings"
	"testing"
)

const sample = `# HELP node_hwmon_temp_celsius Hardware monitor for temperature (input)
# TYPE node_hwmon_temp_celsius gauge
node_hwmon_temp_celsius{chip="platform_coretemp_0",sensor="temp1"} 64
node_hwmon_temp_celsius{chip="platform_coretemp_0",sensor="temp2"} 60
node_hwmon_temp_celsius{chip="platform_coretemp_1",sensor="temp1"} 67
node_hwmon_temp_celsius{chip="nvme_nvme0",sensor="temp1"} 54.85
node_load1 1.7
weird{a="x,y}",b="q\"z"} 3 1700000000000
`

func TestParseAndSelect(t *testing.T) {
	all, err := Parse(strings.NewReader(sample))
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 6 {
		t.Fatalf("got %d series", len(all))
	}
	if all[5].Labels["a"] != "x,y}" || all[5].Labels["b"] != `q"z` || all[5].Value != 3 {
		t.Fatalf("label escaping: %+v", all[5])
	}
	cases := []struct {
		sel  Selector
		want float64
	}{
		{Selector{Name: "node_hwmon_temp_celsius", Labels: map[string]string{"chip": "platform_coretemp_1", "sensor": "temp1"}}, 67},
		{Selector{Name: "node_hwmon_temp_celsius", Labels: map[string]string{"chip": "platform_coretemp_0"}, Agg: "max"}, 64},
		{Selector{Name: "node_hwmon_temp_celsius", Labels: map[string]string{"chip": "platform_coretemp_0"}}, 62},
		{Selector{Name: "node_load1"}, 1.7},
	}
	for _, c := range cases {
		if v, ok := c.sel.Eval(all); !ok || v != c.want {
			t.Errorf("%+v = %v %v, want %v", c.sel, v, ok, c.want)
		}
	}
	if _, ok := (Selector{Name: "missing"}).Eval(all); ok {
		t.Error("missing metric should not match")
	}
}
