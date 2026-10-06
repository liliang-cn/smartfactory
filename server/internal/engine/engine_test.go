package engine

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"
)

func newEngine(t *testing.T) *Engine {
	t.Helper()
	if _, err := os.Stat("js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	e, err := New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestEngineRuns(t *testing.T) {
	e := newEngine(t)
	if len(e.Companies) < 2 {
		t.Fatalf("companies: %v", e.Companies)
	}
	go e.Run(t.Context())

	// Simulate a minute quickly, then check that production and state moved.
	e.SetTimeScale("huaduan", 20)
	time.Sleep(3 * time.Second)
	t.Logf("step cost for %d companies: %v", len(e.Companies), e.StepCost)

	var snap struct {
		Plants []struct {
			ID     string         `json:"id"`
			Kpis   map[string]any `json:"kpis"`
			Assets []struct {
				ID    string  `json:"id"`
				Temp  float64 `json:"temp"`
				State string  `json:"state"`
				Live  string  `json:"live"`
			} `json:"assets"`
		} `json:"plants"`
	}
	if err := json.Unmarshal([]byte(e.Snapshot("huaduan")), &snap); err != nil || len(snap.Plants) != 3 {
		t.Fatalf("snapshot: %v %+v", err, snap)
	}
	if e, _ := snap.Plants[0].Kpis["energyKWh"].(float64); e <= 0 {
		t.Errorf("simulation did not advance: %v", snap.Plants[0].Kpis)
	}

	r := e.Command("huaduan", "p1", "HP-101", "stop", nil)
	if !r.OK {
		t.Fatalf("command: %+v", r)
	}
	e.Ingest("huaduan", "p1", "HP-201", "temp", 79.5, "test")
	time.Sleep(300 * time.Millisecond)

	frames, _, stop := e.Watch("huaduan", "p1")
	defer stop()
	select {
	case f := <-frames:
		var msg struct {
			Data struct {
				TimeScale float64 `json:"timeScale"`
				Plants    map[string]struct {
					Assets []struct {
						ID      string                `json:"id"`
						Enabled bool                  `json:"enabled"`
						Temp    float64               `json:"temp"`
						Live    *struct{ Src string } `json:"live"`
					} `json:"assets"`
					Lines []struct {
						Products [][]any `json:"products"`
					} `json:"lines"`
				} `json:"plants"`
			} `json:"data"`
		}
		if err := json.Unmarshal(f, &msg); err != nil {
			t.Fatal(err)
		}
		t.Logf("frame size %d bytes", len(f))
		p1 := msg.Data.Plants["p1"]
		found := 0
		for _, a := range p1.Assets {
			switch a.ID {
			case "HP-101":
				found++
				if a.Enabled {
					t.Error("HP-101 should be stopped")
				}
			case "HP-201":
				found++
				if a.Live == nil || a.Live.Src != "test" || a.Temp != 79.5 {
					t.Errorf("HP-201 live value not applied: %+v", a)
				}
			}
		}
		if found != 2 || len(p1.Lines) == 0 || len(p1.Lines[0].Products) == 0 {
			t.Errorf("frame content: found=%d lines=%d", found, len(p1.Lines))
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no frame")
	}
}

func discard() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }
