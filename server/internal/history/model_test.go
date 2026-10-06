package history

import (
	"strings"
	"testing"

	semantic "github.com/liliang-cn/semantic-go"
)

// The model must load and compile the questions a forging plant manager actually asks, sliced by
// the dimensions they slice by; a fan-out the compiler refuses here would be refused in front of
// the advisor too.
func TestModelCompiles(t *testing.T) {
	m, err := semantic.Load(Model)
	if err != nil {
		t.Fatalf("load model: %v", err)
	}
	week := []semantic.Filter{{Dimension: "time", Op: ">=", Values: []any{"2026-09-01"}}, {Dimension: "time", Op: "<", Values: []any{"2026-09-08"}}}
	cases := []struct {
		name string
		q    semantic.Query
	}{
		{"oee by line", semantic.Query{Metrics: []string{"oee", "availability", "performance", "first_pass_yield"}, GroupBy: []string{"line_name"}}},
		{"daily scrap of one line", semantic.Query{Metrics: []string{"scrap_rate", "produced_units"}, GroupBy: []string{"time"}, TimeGrain: "day",
			Where: append(week, semantic.Filter{Dimension: "line_name", Op: "=", Values: []any{"曲轴 2 线"}})}},
		{"energy per tonne by plant", semantic.Query{Metrics: []string{"kwh_per_tonne", "gas_per_tonne"}, GroupBy: []string{"plant_name"}}},
		{"mtbf mttr by machine kind", semantic.Query{Metrics: []string{"mtbf_hours", "mttr_minutes"}, GroupBy: []string{"machine_kind"}}},
		{"downtime by reason", semantic.Query{Metrics: []string{"downtime_minutes", "downtime_events"}, GroupBy: []string{"downtime_category", "downtime_reason"}, OrderBy: "downtime_minutes", Descending: true, Limit: 10}},
		{"die life by machine", semantic.Query{Metrics: []string{"avg_tool_hits", "tool_changes", "avg_changeover_minutes"}, GroupBy: []string{"machine", "change_trigger"}}},
		{"defects by type and line", semantic.Query{Metrics: []string{"defects"}, GroupBy: []string{"line_name", "defect_type"}}},
		{"spc mean by feature", semantic.Query{Metrics: []string{"measure_mean", "out_of_spec_rate"}, GroupBy: []string{"machine", "feature"}}},
		{"spares", semantic.Query{Metrics: []string{"spare_min_stock", "spare_stockout_hours"}, GroupBy: []string{"plant_name", "spare_item"}}},
		{"on-time by customer", semantic.Query{Metrics: []string{"on_time_rate", "orders_completed"}, GroupBy: []string{"customer"}}},
		{"scrap rate by supplier", semantic.Query{Metrics: []string{"lot_scrap_rate", "lots_used"}, GroupBy: []string{"supplier"}}},
		{"defects by heat", semantic.Query{Metrics: []string{"defects"}, GroupBy: []string{"defect_supplier", "defect_type"}}},
		{"material yield by part and shift", semantic.Query{Metrics: []string{"material_yield", "good_tonnes"}, GroupBy: []string{"part_name", "shift"}}},
	}
	for _, c := range cases {
		out, err := semantic.Compile(m, c.q, semantic.Postgres{})
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if !strings.Contains(out.SQL, "SELECT") {
			t.Errorf("%s: no SQL", c.name)
		}
	}
}
