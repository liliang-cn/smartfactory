package agents

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"smartfactory/internal/engine"
	"smartfactory/internal/series"
	"smartfactory/internal/tags"
)

// The assistant's machine_trend: a machine's last hours with its stops and why, named the way
// the twin names it; every machine ranked by stop time; a machine only simulated, or a company
// with no equipment data, is said to have no history rather than answered with nothing.
func TestMachineTrendTool(t *testing.T) {
	if _, err := os.Stat("../engine/js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng, err := engine.New(log)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go eng.Run(ctx)
	time.Sleep(300 * time.Millisecond)

	rd, err := series.Open(t.TempDir(), series.Retention{Raw: 14 * 24 * time.Hour, Minute: 90 * 24 * time.Hour, Hour: 365 * 24 * time.Hour}, log)
	if err != nil {
		t.Fatal(err)
	}
	defer rd.Close()
	start := time.Now().Add(-3 * time.Hour).Truncate(time.Minute)
	put := func(asset string, m int, field string, v any) {
		rd.Add(tags.Tag{Company: "huaduan", Plant: "p1", Asset: asset, Field: field, Value: v, Source: "plc", At: start.Add(time.Duration(m) * time.Minute)})
	}
	for m := 0; m < 180; m++ {
		put("HP-101", m, "temp", 50+float64(m)/10)
		put("HP-101", m, "power", 300.0)
		put("TP-101", m, "temp", 45.0)
	}
	put("HP-101", 0, "state", "running")
	put("HP-101", 60, "state", "fault")
	put("HP-101", 60, "fault", "滑块平衡缸压力低")
	put("HP-101", 85, "state", "running")
	put("HP-101", 85, "fault", "")
	put("TP-101", 0, "state", "running")
	put("TP-101", 100, "state", "changeover")
	put("TP-101", 105, "state", "running")
	if err := rd.Flush(); err != nil {
		t.Fatal(err)
	}

	tool := trendTool(rd, eng, "huaduan")
	v, err := tool.run(t.Context(), args{"asset": "hp101", "hours": 4.0})
	if err != nil {
		t.Fatal(err)
	}
	got := v.(map[string]any)
	if got["asset"] != "HP-101" || got["name"] == "" || got["plant"] == "p1" {
		t.Errorf("named as the twin names it: %v / %v / %v", got["asset"], got["name"], got["plant"])
	}
	st := got["states"].(map[string]any)
	stops := st["longest_stops"].([]string)
	if len(stops) != 1 || !strings.Contains(stops[0], "故障 25.0 分钟：滑块平衡缸压力低") {
		t.Errorf("stops: %v", stops)
	}
	fs := got["fields"].([]series.FieldTrend)
	if len(fs) != 2 || fs[0].Field != "temp" || fs[0].Max.V < 67 || fs[1].Field != "power" {
		t.Errorf("fields: %+v", fs)
	}
	for _, p := range fs[0].Curve {
		if at := p[0].(time.Time); at.Second() != 0 || at.Minute()%10 != 0 {
			t.Errorf("curve point at %s, want on the clock", at.Format("15:04:05.000"))
			break
		}
	}

	v, err = tool.run(t.Context(), args{"from": start.In(shanghai).Format("2006-01-02 15:04")})
	if err != nil {
		t.Fatal(err)
	}
	rank := v.(map[string]any)["machines"].([]map[string]any)
	if len(rank) != 2 || rank[0]["asset"] != "HP-101" || rank[0]["stop_minutes"] != 25.0 || rank[1]["asset"] != "TP-101" {
		t.Errorf("ranking: %v", rank)
	}

	if _, err := tool.run(t.Context(), args{"asset": "RF-101"}); err == nil || !strings.Contains(err.Error(), "只有仿真") {
		t.Errorf("a simulated machine: %v", err)
	}
	if _, err := trendTool(rd, eng, "tiankong").run(t.Context(), args{}); err == nil || !strings.Contains(err.Error(), "没有接入设备数据") {
		t.Errorf("a company with no equipment data: %v", err)
	}
	if _, err := tool.run(t.Context(), args{"asset": "HP-101", "from": "昨天晚上"}); err == nil {
		t.Error("a time it cannot read was taken")
	}
}
