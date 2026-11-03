package engine

import (
	"log/slog"
	"os"
	"testing"
	"time"
)

// The plan model has every line with what it makes, its rate, its changeover and its calendar.
func TestPlanModel(t *testing.T) {
	if _, err := os.Stat("js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	e, err := NewOffline(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	if err != nil {
		t.Fatal(err)
	}
	m := e.PlanModel("huaduan")
	if m == nil {
		t.Fatalf("no model: %.600s", e.str("planModel", "huaduan"))
	}
	l := m.Plants[0].Lines[0]
	if l.Neck <= 0 || len(l.Parts) != 3 || l.ToolChange < 3600 || len(l.Tools) == 0 {
		t.Errorf("line %+v", l)
	}
	if len(m.Plants[0].Calendar.Breaks) != 3 || m.Plants[0].Calendar.PM == nil {
		t.Errorf("calendar %+v", m.Plants[0].Calendar)
	}
}

// A plan run takes the plan's orders line by line, in turn, and says when each started and finished.
func TestPlanRun(t *testing.T) {
	if _, err := os.Stat("js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	e, err := NewOffline(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))
	if err != nil {
		t.Fatal(err)
	}
	q := map[string][]ExtOrder{"p1/L5": {{No: "A", PartNo: "LG-B20", Qty: 40}, {No: "B", PartNo: "LG-V30", Qty: 40}}}
	start := time.Now()
	r, err := e.PlanRun("huaduan", q, 6, 2)
	if err != nil {
		t.Fatal(err)
	}
	var seq []string
	for _, ev := range r.Events {
		if ev.Kind == "done" {
			seq = append(seq, ev.No)
		}
	}
	t.Logf("%.1f h of plant time in %s", r.Hours, time.Since(start))
	if len(seq) != 2 || seq[0] != "A" || seq[1] != "B" || r.Hours >= 6 {
		t.Fatalf("done %v in %.1f h: %+v", seq, r.Hours, r.Events)
	}
}
