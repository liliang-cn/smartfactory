package engine

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"testing"
)

type areaModel struct {
	Plants []struct {
		ID    string `json:"id"`
		Hall  string `json:"hall"`
		Areas []struct {
			Code  string   `json:"code"`
			Name  string   `json:"name"`
			Lines []string `json:"lines"`
		} `json:"areas"`
		Lines []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			Area     string `json:"area"`
			Stations []struct {
				ID string `json:"id"`
			} `json:"stations"`
		} `json:"lines"`
	} `json:"plants"`
}

func offlineEngine(t *testing.T) *Engine {
	t.Helper()
	if _, err := os.Stat("js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	e, err := NewOffline(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

// A plant that lists no areas has one, named after its hall, holding every line: every built-in
// company keeps its structure, and its breakdown adds only that level.
func TestAreasDefault(t *testing.T) {
	e := offlineEngine(t)
	for _, id := range e.CompanyIDs() {
		var m areaModel
		if err := json.Unmarshal([]byte(e.ProcessModel(id)), &m); err != nil {
			t.Fatal(id, err)
		}
		tree, err := e.PBS(id)
		if err != nil {
			t.Fatal(id, err)
		}
		for i, p := range m.Plants {
			if len(p.Areas) != 1 || p.Areas[0].Name != p.Hall || p.Areas[0].Code != "A1" || len(p.Areas[0].Lines) != len(p.Lines) {
				t.Errorf("%s/%s areas: %+v", id, p.ID, p.Areas)
			}
			machines := 0
			for _, l := range p.Lines {
				if l.Area != "A1" {
					t.Errorf("%s/%s %s is in area %q", id, p.ID, l.ID, l.Area)
				}
				machines += len(l.Stations)
			}
			pn := tree.Children[i]
			if pn.Kind != "plant" || len(pn.Children) != 1 || pn.Children[0].Kind != "area" || pn.Children[0].Name != p.Hall ||
				pn.Lines != len(p.Lines) || pn.Machines != machines || len(pn.Children[0].Children) != len(p.Lines) {
				t.Errorf("%s/%s breakdown: %+v", id, p.ID, pn)
			}
		}
	}
}

// A plant with areas: lines go to the area they name (or the first), keep their numbers in the
// order written whichever area they are in, and a line naming an area not listed is refused.
func TestAreas(t *testing.T) {
	e := offlineEngine(t)
	var c map[string]any
	json.Unmarshal([]byte(e.BuiltinTemplate("huaduan")), &c)
	c["id"], c["name"], c["short"] = "areas", "车间测试", "车间"
	plant := c["plants"].([]any)[0].(map[string]any)
	plant["areas"] = []any{
		map[string]any{"name": "曲轴车间", "code": "CS"},
		map[string]any{"name": "连杆车间"},
		map[string]any{"name": "成品库"},
	}
	lines := plant["lines"].([]any)
	lines[0].(map[string]any)["area"] = "曲轴车间"
	lines[1].(map[string]any)["area"] = "连杆车间"
	raw, _ := json.Marshal(c)

	before := e.ProcessModel("huaduan")
	if r := e.CreateCompany(raw); !r.OK {
		t.Fatal(r.Errors)
	}
	var m, old areaModel
	json.Unmarshal([]byte(e.ProcessModel("areas")), &m)
	json.Unmarshal([]byte(before), &old)
	p := m.Plants[0]
	names := []string{}
	for _, a := range p.Areas {
		names = append(names, a.Code+" "+a.Name)
	}
	if strings.Join(names, ",") != "CS 曲轴车间,A2 连杆车间,A3 成品库" {
		t.Fatalf("areas: %v", names)
	}
	if len(p.Areas[0].Lines) != 4 || len(p.Areas[1].Lines) != 2 || len(p.Areas[2].Lines) != 0 {
		t.Errorf("lines by area: %+v", p.Areas)
	}
	// the same lines and machine tags as the company without areas
	for i, l := range p.Lines {
		ol := old.Plants[0].Lines[i]
		if l.ID != ol.ID || len(l.Stations) != len(ol.Stations) || l.Stations[0].ID != ol.Stations[0].ID {
			t.Errorf("line %d: %s %s, was %s %s", i, l.ID, l.Stations[0].ID, ol.ID, ol.Stations[0].ID)
		}
	}

	tree, err := e.PBS("areas")
	if err != nil {
		t.Fatal(err)
	}
	pn := tree.Children[0]
	if len(pn.Children) != 3 || pn.Children[1].Name != "连杆车间" || pn.Children[1].Lines != 2 || pn.Children[2].Machines != 0 {
		t.Errorf("breakdown: %+v", pn.Children)
	}
	for _, an := range pn.Children {
		for _, ln := range an.Children {
			for _, mn := range ln.Children {
				if mn.Kind != "machine" || mn.State == "" || mn.States[mn.State] != 1 {
					t.Errorf("machine %+v", mn)
				}
			}
		}
	}

	// lines written out of area order keep their numbers
	lines[0].(map[string]any)["area"] = "连杆车间"
	lines[1].(map[string]any)["area"] = "曲轴车间"
	raw, _ = json.Marshal(c)
	if r := e.CreateCompany(raw); !r.OK {
		t.Fatal(r.Errors)
	}
	json.Unmarshal([]byte(e.ProcessModel("areas")), &m)
	if got := m.Plants[0].Lines[0]; got.ID != "L1" || got.Area != "A2" || !slices.Contains(m.Plants[0].Areas[1].Lines, "L1") {
		t.Errorf("first line: %+v", got)
	}

	lines[0].(map[string]any)["area"] = "压铸车间"
	raw, _ = json.Marshal(c)
	if r := e.CheckCompany(raw); r.OK || !strings.Contains(strings.Join(r.Errors, " "), "压铸车间") {
		t.Errorf("a line in an area not listed: %+v", r)
	}
	plant["areas"] = []any{map[string]any{"name": "曲轴车间"}, map[string]any{"name": "曲轴车间"}}
	delete(lines[0].(map[string]any), "area")
	delete(lines[1].(map[string]any), "area")
	raw, _ = json.Marshal(c)
	if r := e.CheckCompany(raw); r.OK || !strings.Contains(strings.Join(r.Errors, " "), "重复") {
		t.Errorf("two areas of one name: %+v", r)
	}
}
