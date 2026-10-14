package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

// The 串串香 chain: its kitchen and shops lay out and run, the cold rooms and fridges hold
// 0–4 ℃ (the freezers below −18 ℃), a fridge that is stopped or broken warms up without
// tripping and comes back, and the shops take in no customers outside their opening hours.
func TestChuanchuan(t *testing.T) {
	if testing.Short() {
		t.Skip("a day of plant time")
	}
	if _, err := os.Stat("js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	e, err := NewOffline(discard())
	if err != nil {
		t.Fatal(err)
	}
	const c = "qianyu"
	if !strings.Contains(strings.Join(e.CompanyIDs(), " "), c) {
		t.Fatalf("%s not loaded: %v", c, e.CompanyIDs())
	}
	for _, id := range []string{"chuanchuan", "chuanchuan-store"} {
		var p map[string]any
		json.Unmarshal([]byte(e.Industry(id)), &p)
		p["id"] = "my-" + id
		b, _ := json.Marshal(p)
		if r := e.CheckIndustry(b); !r.OK {
			t.Errorf("a copy of %s: %v", id, r.Errors)
		}
	}

	type asset struct {
		ID, State string
		Temp      float64
	}
	type plant struct {
		ID   string
		Kpis struct{ Clock string }
		// units on each line now
		Lines  []struct{ Good float64 }
		Assets []asset
	}
	snap := func() map[string]plant {
		var s struct{ Plants []plant }
		if err := json.Unmarshal([]byte(e.Snapshot(c)), &s); err != nil {
			t.Fatal(err)
		}
		out := map[string]plant{}
		for _, p := range s.Plants {
			out[p.ID] = p
		}
		return out
	}
	units := func() map[string]int {
		var f struct {
			Plants map[string]struct {
				Lines []struct {
					Products []json.RawMessage `json:"products"`
				} `json:"lines"`
			} `json:"plants"`
		}
		v, err := e.call("frame", c)
		if err != nil {
			t.Fatal(err)
		}
		json.Unmarshal([]byte(v.String()), &f)
		out := map[string]int{}
		for id, p := range f.Plants {
			for _, l := range p.Lines {
				out[id] += len(l.Products)
			}
		}
		return out
	}
	ff := func(sec float64) {
		t.Helper()
		if _, err := e.call("fastForward", c, sec); err != nil {
			t.Fatal(err)
		}
	}
	find := func(plant, id string) asset {
		t.Helper()
		for _, a := range snap()[plant].Assets {
			if a.ID == id {
				return a
			}
		}
		t.Fatalf("no %s in %s", id, plant)
		return asset{}
	}

	ff(600)
	for pid, p := range snap() {
		for _, a := range p.Assets {
			if a.State == "fault" || a.State == "maint" {
				continue
			}
			switch strings.SplitN(a.ID, "-", 2)[0] {
			case "LG", "LK": // display fridges, cold rooms
				if a.Temp < 0 || a.Temp > 4 {
					t.Errorf("%s/%s at %.1f ℃, want 0–4", pid, a.ID, a.Temp)
				}
			case "LD", "SD": // freezer room, blast freezer
				if a.Temp > -18 {
					t.Errorf("%s/%s at %.1f ℃, want −18 or below", pid, a.ID, a.Temp)
				}
			}
		}
	}

	if r := e.Command(c, "chunxi", "LG-101", "stop", nil); !r.OK {
		t.Fatal(r.Msg)
	}
	if r := e.Command(c, "chunxi", "LG-201", "fault", nil); !r.OK {
		t.Fatal(r.Msg)
	}
	ff(1800)
	if a := find("chunxi", "LG-101"); a.Temp < 8 || a.State == "fault" {
		t.Errorf("a fridge stopped for half an hour: %+v, want warm and not tripped", a)
	}
	e.Command(c, "chunxi", "LG-101", "start", nil)
	ff(3600)
	for _, id := range []string{"LG-101", "LG-201"} {
		if a := find("chunxi", id); a.Temp > 4 || a.State == "fault" {
			t.Errorf("%s an hour after it was started again or broke down: %+v, want repaired and cold", id, a)
		}
	}

	// to a given hour of the plant's (China) day
	until := func(hour int) {
		t.Helper()
		var h, m int
		if clock := snap()["chunxi"].Kpis.Clock; func() bool { _, err := fmt.Sscanf(clock, "%d:%d", &h, &m); return err != nil }() {
			t.Fatalf("clock %q", clock)
		}
		left := ((hour*60-h*60-m)%1440 + 1440) % 1440
		// in steps of 2 s, nobody watching: a day at 0.5 s steps takes minutes
		if !e.Advance(c, float64(left*60), 2) {
			t.Fatal("advance")
		}
	}
	good := func() map[string]float64 {
		out := map[string]float64{}
		for id, p := range snap() {
			for _, l := range p.Lines {
				out[id] += l.Good
			}
		}
		return out
	}

	// 03:00: the shops closed at 01:00 and the kitchen at 16:00, their lines are empty
	until(3)
	for id, n := range units() {
		if n != 0 {
			t.Errorf("%s has %d units on its lines at 03:00, closed", id, n)
		}
	}
	before := good()
	// 13:00: everyone is open and working
	until(13)
	after, now := good(), units()
	for _, id := range []string{"kitchen", "chunxi", "yulin"} {
		if after[id] <= before[id] || now[id] == 0 {
			t.Errorf("%s at 13:00: made %v → %v, %d units on its lines", id, before[id], after[id], now[id])
		}
	}
}
