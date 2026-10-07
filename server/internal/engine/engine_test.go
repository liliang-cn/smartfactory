package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
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
	if len(e.CompanyIDs()) < 2 {
		t.Fatalf("companies: %v", e.CompanyIDs())
	}
	go e.Run(t.Context())

	// Simulate a minute quickly, then check that production and state moved.
	e.SetTimeScale("huaduan", 20)
	time.Sleep(3 * time.Second)
	t.Logf("step cost for %d companies: %v", len(e.CompanyIDs()), e.StepCost())

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

func TestPersistence(t *testing.T) {
	dir := t.TempDir()
	e := newEngine(t)
	e.Dir = dir
	ctx, cancel := context.WithCancel(context.Background())
	go e.Run(ctx)
	e.SetTimeScale("huaduan", 20)
	time.Sleep(2 * time.Second)
	if r := e.Command("huaduan", "p2", "CV-1", "stop", nil); !r.OK {
		t.Fatal(r.Msg)
	}
	cancel()
	<-e.Done
	before := kpis(t, e, "huaduan", "p2")

	// A fresh engine picks up where the first one stopped.
	e2 := newEngine(t)
	e2.Dir = dir
	e2.Restore()
	after := kpis(t, e2, "huaduan", "p2")
	if before.Clock != after.Clock || before.Good != after.Good || after.Good == 0 {
		t.Fatalf("not restored: before %+v after %+v", before, after)
	}
	if after.CV1 {
		t.Error("CV-1 should still be stopped after restore")
	}
	// work orders, steel heats and document numbers carry on too
	saved, _ := os.ReadFile(filepath.Join(dir, "huaduan.json"))
	carried := regexp.MustCompile(`"job":\{"no":"[^"]+"|"lot":\{"no":"[^"]+","supplier":"[^"]*","factor":[0-9.]+,"left":[0-9]+|"seq":[0-9]+`)
	sv, _ := e2.call("save", "huaduan")
	if was, is := carried.FindAllString(string(saved), -1), carried.FindAllString(sv.String(), -1); len(was) < 10 || !slices.Equal(was, is) {
		t.Errorf("work orders and heats not carried on:\nbefore %v\nafter  %v", was, is)
	}
	frame := map[string]any{}
	fv, _ := e2.call("frame", "huaduan")
	json.Unmarshal([]byte(fv.String()), &frame)
	if frame["timeScale"] != 20.0 {
		t.Errorf("time scale not restored: %v", frame["timeScale"])
	}

	// A plant whose structure changed is not restored.
	b, _ := os.ReadFile(filepath.Join(dir, "huaduan.json"))
	os.WriteFile(filepath.Join(dir, "huaduan.json"), []byte(strings.Replace(string(b), `"fp":"L1:`, `"fp":"Lx:`, 1)), 0o644)
	e3 := newEngine(t)
	e3.Dir = dir
	e3.Restore()
	if k := kpis(t, e3, "huaduan", "p1"); k.Clock == before.Clock {
		t.Error("p1 with a changed fingerprint should start fresh")
	}
}

type plantKpis struct {
	Clock string
	Good  float64
	CV1   bool
}

func kpis(t *testing.T, e *Engine, company, plant string) plantKpis {
	t.Helper()
	var f struct {
		Plants map[string]struct {
			Clock  float64 `json:"clock"`
			Assets []struct {
				ID      string `json:"id"`
				Enabled bool   `json:"enabled"`
			} `json:"assets"`
			Lines []struct {
				Good float64 `json:"good"`
			} `json:"lines"`
		} `json:"plants"`
	}
	// the engine loop is not running here: call the JS function directly
	v, err := e.call("frame", company)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(v.String()), &f); err != nil {
		t.Fatal(err)
	}
	p := f.Plants[plant]
	out := plantKpis{Clock: fmt.Sprintf("%.1f", p.Clock)}
	for _, l := range p.Lines {
		out.Good += l.Good
	}
	for _, a := range p.Assets {
		if a.ID == "CV-1" {
			out.CV1 = a.Enabled
		}
	}
	return out
}

// A watcher of one plant gets the other plants only now and then; a watcher of "all" gets every
// plant in every frame.
func TestWatchAll(t *testing.T) {
	e := newEngine(t)
	one, _, stop1 := e.Watch("huaduan", "p1")
	defer stop1()
	all, _, stop2 := e.Watch("huaduan", AllPlants)
	defer stop2()
	plants := func(ch <-chan []byte) []string {
		var msg struct {
			Data struct {
				Plants map[string]json.RawMessage `json:"plants"`
			} `json:"data"`
		}
		select {
		case f := <-ch:
			if err := json.Unmarshal(f, &msg); err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatal("no frame")
		}
		var ids []string
		for id := range msg.Data.Plants {
			ids = append(ids, id)
		}
		return ids
	}
	for i := range 3 {
		e.broadcast()
		a, b := plants(one), plants(all)
		if len(b) != 3 {
			t.Errorf("frame %d: all-plants watcher got %v", i, b)
		}
		// the first frame after subscribing carries everything
		if want := map[bool]int{true: 3, false: 1}[i == 0]; len(a) != want {
			t.Errorf("frame %d: single-plant watcher got %v, want %d plants", i, a, want)
		}
	}
}

// Vehicles located by the plant's fleet system still keep the twin's lines supplied: the twin
// moves the material when a reported vehicle drives off a dock.
func TestLocatedVehiclesKeepLinesSupplied(t *testing.T) {
	if testing.Short() {
		t.Skip("twenty plant minutes")
	}
	twin, plant := newEngine(t), newEngine(t)
	if _, err := plant.call("playEquipment"); err != nil {
		t.Fatal(err)
	}
	stock := func() (bf float64) {
		v, _ := twin.call("save", "huaduan")
		var s struct {
			Plants map[string]struct {
				State struct {
					Assets []struct {
						ID    string  `json:"id"`
						Stock float64 `json:"stock"`
					} `json:"assets"`
				} `json:"state"`
			} `json:"plants"`
		}
		json.Unmarshal([]byte(v.String()), &s)
		for _, a := range s.Plants["p1"].State.Assets {
			if a.ID == "BF-1" {
				bf = a.Stock
			}
		}
		return
	}
	last := stock()
	refills := 0
	for range 600 {
		plant.call("step", 2)
		twin.call("step", 2)
		v, _ := plant.call("telemetry", "huaduan", "p1")
		var tel struct {
			Vehicles []struct {
				ID      string  `json:"id"`
				X       float64 `json:"x"`
				Z       float64 `json:"z"`
				Heading float64 `json:"heading"`
				Task    string  `json:"task"`
			} `json:"vehicles"`
		}
		json.Unmarshal([]byte(v.String()), &tel)
		for _, g := range tel.Vehicles {
			for f, x := range map[string]any{"x": g.X, "z": g.Z, "heading": g.Heading, "task": g.Task} {
				twin.call("ingest", "huaduan", "p1", g.ID, f, x, "dev")
			}
		}
		bf := stock()
		if bf > last {
			refills++
		}
		last = bf
	}
	if refills == 0 {
		t.Errorf("BF-1 was never refilled in twenty minutes, %v left", last)
	}
}

// Equipment reports in bursts far larger than the loop's call queue; none of it is dropped.
func TestIngestBurst(t *testing.T) {
	e := newEngine(t)
	go e.Run(t.Context())
	for i := range 5000 {
		e.Ingest("huaduan", "p1", "HP-201", "temp", 20+float64(i)/100, "test")
	}
	time.Sleep(500 * time.Millisecond)
	var temp float64
	e.do(func() {
		f, _ := e.call("frame", "huaduan")
		var fr struct {
			Plants map[string]struct {
				Assets []struct {
					ID   string  `json:"id"`
					Temp float64 `json:"temp"`
				} `json:"assets"`
			} `json:"plants"`
		}
		json.Unmarshal([]byte(f.String()), &fr)
		for _, a := range fr.Plants["p1"].Assets {
			if a.ID == "HP-201" {
				temp = a.Temp
			}
		}
	})
	if temp != 69.99 {
		t.Errorf("HP-201 temp %v, want the last value 69.99", temp)
	}
}
