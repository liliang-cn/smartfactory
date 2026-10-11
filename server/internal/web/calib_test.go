package web

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"smartfactory/internal/audit"
	"smartfactory/internal/auth"
	"smartfactory/internal/calib"
	"smartfactory/internal/config"
	"smartfactory/internal/engine"
	"smartfactory/internal/series"
	"smartfactory/internal/sfptest"
	"smartfactory/internal/tags"
)

// Calibration through the console's API: a press's stored readings fitted, the fit applied (by
// the API and as a confirmed console action) and taken back; members read residuals but cannot
// change anything; every answer holds to the protocol.
func TestCalibration(t *testing.T) {
	if _, err := os.Stat("../engine/js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	std := sfptest.Load(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng, err := engine.New(log)
	if err != nil {
		t.Fatal(err)
	}
	eng.Dir = t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); <-eng.Done })
	go eng.Run(ctx)
	store, err := series.Open(t.TempDir(), series.RetentionFrom(config.Series{}), log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	trail, _ := audit.Open(t.TempDir(), log)
	t.Cleanup(func() { trail.Close() })
	cs, err := calib.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cs.Close() })
	svc := calib.New(cs, eng, store, log)

	// three hours of HP-101: 20 minutes running, 10 idle, drawing 3 kW idle and 40 running
	now := time.Now()
	temp := 30.0
	for at := now.Add(-3 * time.Hour); at.Before(now); at = at.Add(5 * time.Second) {
		running := at.Sub(now.Add(-3*time.Hour))%(30*time.Minute) < 20*time.Minute
		state, power, target := "idle", 3.0, 22+8.0
		if running {
			state, power, target = "running", 43.0, 22+35.0
		}
		temp += (target - temp) * (1 - math.Exp(-5.0/90))
		for _, x := range []tags.Tag{
			{Field: "state", Value: state}, {Field: "power", Value: power}, {Field: "temp", Value: temp}, {Field: "vib", Value: map[bool]float64{true: 3.1, false: 0.2}[running]},
		} {
			x.Company, x.Plant, x.Asset, x.Source, x.At = "huaduan", "p1", "HP-101", "plc", at
			store.Add(x)
		}
	}
	if err := store.Flush(); err != nil {
		t.Fatal(err)
	}

	admin, ak := auth.NewToken("厂长", auth.Admin, nil, "")
	member, mk := auth.NewToken("计划员", auth.Member, []string{"huaduan"}, "")
	ts := httptest.NewServer((&Server{Hub: tags.NewHub(), Engine: eng, Keys: keyring(t, "", ak, mk), Audit: trail, Log: log, Series: store, Calib: svc}).Handler())
	t.Cleanup(ts.Close)
	do := func(token, method, path string, body any) (int, []byte) {
		t.Helper()
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = strings.NewReader(string(b))
		}
		req, _ := http.NewRequest(method, ts.URL+path, rd)
		req.Header.Set("Authorization", "Bearer "+token)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, b
	}

	got, b := do(admin, "GET", "/api/admin/calib/huaduan", nil)
	if got != 200 {
		t.Fatalf("status: %d %s", got, b)
	}
	std.Check("Calibration", b)
	if got, _ := do(member, "POST", "/api/admin/calib/huaduan/fit", map[string]any{}); got != 403 {
		t.Errorf("a member fitted: %d", got)
	}

	got, b = do(admin, "POST", "/api/admin/calib/huaduan/fit", map[string]any{"machines": []string{"HP-101"}, "hours": 4})
	if got != 200 {
		t.Fatalf("fit: %d %s", got, b)
	}
	std.Check("Calibration", b)
	var fits []calib.Machine
	json.Unmarshal(b, &fits)
	if len(fits) != 1 {
		t.Fatalf("fitted %d machines", len(fits))
	}
	values := map[string]float64{}
	for _, p := range fits[0].Params {
		if p.Fitted != nil {
			values[p.Name] = *p.Fitted
		}
	}
	for name, want := range map[string]float64{"pIdle": 3, "pRun": 40, "idleRise": 8, "runRise": 35 / (0.6 + 0.4)} {
		if math.Abs(values[name]-want) > 0.05*want+0.2 {
			t.Errorf("%s fitted %.2f, want %.2f (all: %v)", name, values[name], want, values)
		}
	}
	if got, _ := do(admin, "POST", "/api/admin/calib/huaduan/fit", map[string]any{"machines": []string{"XX-999"}}); got != 404 {
		t.Errorf("fitting a machine that does not exist: %d", got)
	}

	got, b = do(admin, "POST", "/api/admin/calib/huaduan/apply", map[string]any{"machines": []string{"HP-101"}})
	if got != 200 {
		t.Fatalf("apply: %d %s", got, b)
	}
	std.Check("CalibOutcomes", b)
	applied := false
	for _, mp := range eng.MachineParams("huaduan") {
		if mp.Plant == "p1" && mp.Asset == "HP-101" && mp.Calib != nil && mp.Calib.PIdle != nil && math.Abs(*mp.Calib.PIdle-3) < 0.2 {
			applied = true
		}
	}
	if !applied {
		t.Error("the engine does not run HP-101 on its fitted parameters")
	}

	got, b = do(member, "GET", "/api/calib/huaduan/residuals?machine=HP-101&field=power", nil)
	if got != 200 {
		t.Fatalf("residuals: %d %s", got, b)
	}
	std.Check("Residuals", b)

	got, b = do(admin, "POST", "/api/admin/calib/huaduan/revert", nil)
	if got != 200 || !strings.Contains(string(b), `"ok": true`) {
		t.Fatalf("revert: %d %s", got, b)
	}
	std.Check("CalibOutcomes", b)

	// the console agent's proposal, confirmed
	got, b = do(admin, "POST", "/api/admin/actions", map[string]any{"kind": "apply_calibration", "args": map[string]any{"company": "huaduan", "machines": []string{"HP-101"}}})
	if got != 200 || !strings.Contains(string(b), `"ok": true`) {
		t.Fatalf("action: %d %s", got, b)
	}

	entries, _ := trail.List(t.Context(), "huaduan", 0, 50)
	var actions []string
	for _, e := range entries {
		actions = append(actions, e.Action)
	}
	for _, want := range []string{"calib.fit", "calib.apply", "calib.revert"} {
		if !strings.Contains(strings.Join(actions, ","), want) {
			t.Errorf("not in the activity log: %s (have %v)", want, actions)
		}
	}
}
