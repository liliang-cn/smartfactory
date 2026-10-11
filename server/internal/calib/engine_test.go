package calib

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"smartfactory/internal/engine"
)

// A press that draws more and runs hotter than its template reports to the twin: its model's
// residuals are large; once its fitted parameters are applied they are small, the what-if says
// it runs on fitted parameters, and taking the calibration back restores the template.
func TestApplyChangesTheTwin(t *testing.T) {
	if _, err := os.Stat("../engine/js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng, err := engine.New(log)
	if err != nil {
		t.Fatal(err)
	}
	eng.Dir = t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); <-eng.Done })
	go eng.Run(ctx)

	var tpl engine.MachineParams
	for _, mp := range eng.MachineParams("huaduan") {
		if mp.Plant == "p1" && mp.Asset == "HP-101" {
			tpl = mp
		}
	}
	if tpl.Asset == "" {
		t.Fatal("no HP-101 in huaduan p1")
	}
	// what the press really does
	realIdle, realRun, realRise := *tpl.Template.PIdle*2, *tpl.Template.PRun*1.5, *tpl.Template.RunRise*1.3
	temp := ambient + realRise // running at full speed and health
	report := func(seconds float64) {
		for left := seconds; left > 0; left -= 30 {
			for _, f := range []struct {
				k string
				v any
			}{{"state", "running"}, {"speed", 1.0}, {"health", 1.0}, {"power", realIdle + realRun}, {"temp", temp}, {"vib", *tpl.Template.Vib}} {
				eng.Ingest("huaduan", "p1", "HP-101", f.k, f.v, "test")
			}
			time.Sleep(20 * time.Millisecond) // the engine takes them in
			eng.FastForward("huaduan", 30)
		}
	}
	// residuals drains what the models got wrong, per field (mean absolute error)
	residuals := func() map[string]float64 {
		sec, abs := map[string]float64{}, map[string]float64{}
		for _, r := range eng.Residuals("huaduan") {
			if r.Asset == "HP-101" && r.Plant == "p1" {
				sec[r.Field] += r.Seconds
				abs[r.Field] += r.MAE * r.Seconds
			}
		}
		out := map[string]float64{}
		for f := range sec {
			out[f] = abs[f] / sec[f]
		}
		if len(out) < 3 {
			t.Fatalf("residuals for %v only", out)
		}
		return out
	}

	report(60)
	eng.Residuals("huaduan") // the shadow model settles first
	report(600)
	before := residuals()
	beforePower, beforeTemp := before["power"], before["temp"]

	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	s := New(st, eng, nil, log)
	fit := []Param{
		{Name: "pIdle", Fitted: f64(realIdle)}, {Name: "pRun", Fitted: f64(realRun)}, {Name: "runRise", Fitted: f64(realRise)},
		{Name: "tau", Template: 40}, // not fitted: stays the template's
	}
	if err := st.saveFit("huaduan", "p1", "HP-101", Fitted{At: time.Now(), Params: fit}); err != nil {
		t.Fatal(err)
	}
	out, err := s.Apply("huaduan", []string{"HP-101"}, "测试")
	if err != nil || len(out) != 1 || !out[0].OK || out[0].Params != 3 {
		t.Fatalf("apply: %+v %v", out, err)
	}
	for _, mp := range eng.MachineParams("huaduan") {
		if mp.Asset == "HP-101" && mp.Plant == "p1" && (mp.Calib == nil || mp.Calib.PIdle == nil || *mp.Calib.PIdle != realIdle || mp.Calib.Tau != nil) {
			t.Fatalf("the engine's HP-101 after apply: %+v", mp.Calib)
		}
	}
	eng.Residuals("huaduan")
	report(600)
	after := residuals()
	afterPower, afterTemp := after["power"], after["temp"]
	t.Logf("power MAE %.2f → %.2f kW, temperature MAE %.2f → %.2f °C", beforePower, afterPower, beforeTemp, afterTemp)
	if !(afterPower < beforePower/10) || !(afterTemp < beforeTemp/3) {
		t.Errorf("residuals after the calibration: power %.2f → %.2f, temp %.2f → %.2f", beforePower, afterPower, beforeTemp, afterTemp)
	}

	// a what-if of the plant runs on the fitted parameters, and says so
	var wi map[string]any
	json.Unmarshal([]byte(eng.WhatIf("huaduan", "p1", []string{}, 0.02, 1, []any{}, 1, 1)), &wi)
	if cal, _ := json.Marshal(wi["校准"]); !strings.Contains(string(cal), "HP-101") {
		t.Errorf("what-if does not say HP-101 is calibrated: %s (%.300s)", cal, eng.WhatIf("huaduan", "p1", []string{}, 0.02, 1, []any{}, 1, 1))
	}

	// the calibration survives a save and restore (and so a what-if copy)
	saved := eng.SaveJSON("huaduan")
	if !strings.Contains(saved, `"calib"`) {
		t.Error("the saved state does not carry the calibration")
	}

	out, err = s.Revert("huaduan", nil)
	if err != nil || len(out) != 1 || !out[0].OK {
		t.Fatalf("revert: %+v %v", out, err)
	}
	for _, mp := range eng.MachineParams("huaduan") {
		if mp.Asset == "HP-101" && mp.Plant == "p1" && mp.Calib != nil {
			t.Fatalf("HP-101 still calibrated after revert: %+v", mp.Calib)
		}
	}
	eng.Residuals("huaduan")
	report(300)
	if again := residuals()["power"]; again < beforePower/2 {
		t.Errorf("after revert the power residual is %.2f, it was %.2f with the template", again, beforePower)
	}
}
