package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"
)

// shanghai is the plants' clock.
var shanghai = time.FixedZone("CST", 8*3600)

// at starts a company's plants at a local wall-clock moment (Asia/Shanghai).
func at(t *testing.T, e *Engine, c string, when time.Time) {
	t.Helper()
	if _, err := e.call("setClock", c, float64(when.UnixMilli())); err != nil {
		t.Fatal(err)
	}
}

type figure struct {
	ID, Label, Text, Sub string
	Value                float64
}

type place struct {
	ID, Area, State, Source string
	Seats, People, Minutes  int
}

type venueSnap struct {
	Places, Occupied, Queue, Waiting int
	Occupancy, Turnover, Wait        float64
	Day                              map[string]float64
}

type plantSnap struct {
	ID      string
	Kpis    struct{ Clock string }
	Figures []figure
	Places  []place
	Queue   []struct{ People, Minutes int }
	Venue   *venueSnap
	Depot   *struct {
		Otif, Deliveries, Samples, Wasted, Stock float64
		Batches                                  []struct {
			No, Product string
			Qty         int
			HoursLeft   float64
		}
	}
	Incoming []struct {
		No    string
		Items map[string]int
	}
	Inventory []struct {
		Key       string
		Available int
		Capacity  int
	}
	Shift, Pause string
	Lines        []struct {
		Name string
		Oee  float64
		Good int
	}
}

func snapOf(t *testing.T, e *Engine, c string) map[string]plantSnap {
	t.Helper()
	var s struct{ Plants []plantSnap }
	if v, err := e.call("snapshot", c); err != nil {
		t.Fatal(err)
	} else if err := json.Unmarshal([]byte(v.String()), &s); err != nil {
		t.Fatal(err)
	}
	out := map[string]plantSnap{}
	for _, p := range s.Plants {
		out[p.ID] = p
	}
	return out
}

func offline(t *testing.T, c string) *Engine {
	t.Helper()
	if _, err := os.Stat("js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	e, err := NewOffline(discard())
	if err != nil {
		t.Fatal(err)
	}
	if err := e.Only([]string{c}); err != nil {
		t.Fatal(err)
	}
	return e
}

func TestDebugWarm(t *testing.T) {
	if os.Getenv("SF_DEBUG") == "" {
		t.Skip("debug")
	}
	e := offline(t, "qianyu")
	for i := 0; i < 3; i++ {
		v, _ := e.call("plantFrame", "qianyu", "chunxi")
		s := v.String()
		t.Log(s[:min(len(s), 300)])
		var f struct {
			Lines []struct {
				Products [][]any
				Slots    []int
			}
		}
		json.Unmarshal([]byte(s), &f)
		for _, l := range f.Lines {
			t.Logf("%v %v", l.Products, l.Slots)
		}
		e.Advance("qianyu", 5, 0.5)
	}
}

func TestDebugCamp(t *testing.T) {
	if os.Getenv("SF_DEBUG") == "" {
		t.Skip("debug")
	}
	e := offline(t, "tiankong")
	at(t, e, "tiankong", time.Date(2026, 10, 16, 12, 0, 0, 0, shanghai))
	for h := 0; h < 30; h += 2 {
		e.Advance("tiankong", 7200, 2)
		for _, p := range snapOf(t, e, "tiankong") {
			line := p.Kpis.Clock + " " + p.ID
			for _, f := range p.Figures {
				line += " | " + f.Label + " " + f.Text
			}
			t.Log(line)
		}
	}
}

func TestDebugShopDay(t *testing.T) {
	if os.Getenv("SF_DEBUG") == "" {
		t.Skip("debug")
	}
	e := offline(t, "qianyu")
	at(t, e, "qianyu", time.Date(2026, 10, 17, 9, 0, 0, 0, shanghai))
	for h := 0; h < 16; h++ {
		e.Advance("qianyu", 3600, 1)
		s := snapOf(t, e, "qianyu")
		for _, id := range []string{"chunxi", "kitchen"} {
			p := s[id]
			line := p.Kpis.Clock + " " + id
			for _, f := range p.Figures {
				line += " | " + f.Label + " " + f.Text
			}
			t.Log(line)
		}
		t.Logf("   inv %+v incoming %d", s["chunxi"].Inventory, len(s["chunxi"].Incoming))
		var raw struct {
			Plants []struct {
				ID     string
				Assets []struct {
					ID, State string
					Temp      float64
				}
			}
		}
		v, _ := e.call("snapshot", "qianyu")
		json.Unmarshal([]byte(v.String()), &raw)
		for _, p := range raw.Plants {
			if p.ID != "kitchen" {
				continue
			}
			line := ""
			for _, a := range p.Assets {
				if len(a.ID) > 2 && (a.ID[:2] == "LK" || a.ID[:2] == "LD" || a.ID[:2] == "SD") {
					line += fmt.Sprintf(" %s %s %.1f", a.ID, a.State, a.Temp)
				}
			}
			t.Log("   cold:" + line)
		}
	}
}
