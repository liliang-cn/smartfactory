package engine

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
)

// An industry pack's ontology is checked with the pack: a class under one nobody declared,
// classes in a circle, a relation of an unknown class, a rule over a relation nobody declared,
// a binding to a product or machine the pack does not have each say what is wrong, in Chinese.
// A good one passes, and the company's process model carries it, merged with the company's own,
// with each product and station of a bound kind given its class.
func TestOntologyChecked(t *testing.T) {
	if _, err := os.Stat("js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	e, err := NewOffline(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../knowledge/testdata/chuan-pack.json")
	if err != nil {
		t.Fatal(err)
	}
	pack := func() (map[string]any, map[string]any) {
		var p map[string]any
		json.Unmarshal(raw, &p)
		return p, p["ontology"].(map[string]any)
	}
	enc := func(v any) []byte { b, _ := json.Marshal(v); return b }
	if r := e.CheckIndustry(raw); !r.OK {
		t.Fatalf("the fixture: %v", r.Errors)
	}

	for _, c := range []struct {
		name  string
		break_ func(o map[string]any)
		want  string
	}{
		{"unknown superclass", func(o map[string]any) {
			o["classes"].(map[string]any)["毛肚"] = map[string]any{"sub": []any{"荤菜", "内脏"}}
		}, "ontology.classes.毛肚.sub.1：没有「内脏」这个类"},
		{"a circle", func(o map[string]any) {
			o["classes"].(map[string]any)["食材"] = map[string]any{"sub": "毛肚"}
		}, "类的上下级绕成了圈"},
		{"relation of an unknown class", func(o map[string]any) {
			o["relations"].(map[string]any)["原料"].(map[string]any)["domain"] = "菜式"
		}, "ontology.relations.原料.domain：没有「菜式」这个类"},
		{"rule over an unknown relation", func(o map[string]any) {
			o["rules"] = []any{map[string]any{"id": "r", "if": []any{"原料", "致敏"}, "then": "含过敏原"}}
		}, "ontology.rules.0.if.1：没有「致敏」这个关系"},
		{"bind to an unknown product", func(o map[string]any) {
			o["bind"].(map[string]any)["products"] = map[string]any{"hotpot": "锅底"}
		}, "ontology.bind.products.hotpot：没有产品 hotpot"},
		{"bind to an unknown machine", func(o map[string]any) {
			o["bind"].(map[string]any)["machines"] = map[string]any{"fridge": "冷藏设备"}
		}, "ontology.bind.machines.fridge：没有设备 fridge"},
		{"individual of an unknown class", func(o map[string]any) {
			o["individuals"].(map[string]any)["鸭肠"] = map[string]any{"a": "内脏"}
		}, "ontology.individuals.鸭肠.a.0：没有「内脏」这个类"},
		{"a constraint that checks nothing", func(o map[string]any) {
			o["constraints"] = []any{map[string]any{"id": "空", "class": "冷藏设备", "field": "temp"}}
		}, "要给 min 或 max"},
	} {
		p, o := pack()
		c.break_(o)
		r := e.CheckIndustry(enc(p))
		if r.OK || !strings.Contains(strings.Join(r.Errors, "\n"), c.want) {
			t.Errorf("%s: %+v, want %q", c.name, r.Errors, c.want)
		}
	}

	// a company's own ontology is checked against its packs'
	if r := e.CreateIndustry(raw); !r.OK {
		t.Fatal(r.Errors)
	}
	co, _ := os.ReadFile("../knowledge/testdata/chuan-company.json")
	var company map[string]any
	json.Unmarshal(co, &company)
	company["ontology"] = map[string]any{"bind": map[string]any{"machines": map[string]any{"fryer": "冷藏设备"}}}
	if r := e.CheckCompany(enc(company)); r.OK || !strings.Contains(strings.Join(r.Errors, "\n"), "ontology.bind.machines.fryer") {
		t.Errorf("a company binding a machine its packs lack: %+v", r)
	}
	if r := e.CreateCompany(co); !r.OK {
		t.Fatal(r.Errors)
	}
	var model struct {
		Ontology struct {
			Classes     []struct{ Name string }
			Individuals []struct {
				Name  string
				Facts map[string][]string
			}
		}
		Plants []struct {
			Lines []struct {
				Product struct {
					Key, Individual string
					Classes         []string
				}
				Stations []struct {
					ID, Machine string
					Classes     []string
				}
			}
		}
	}
	json.Unmarshal([]byte(e.ProcessModel("chuan")), &model)
	if len(model.Ontology.Classes) < 9 {
		t.Errorf("classes: %+v", model.Ontology.Classes)
	}
	merged := false
	for _, ind := range model.Ontology.Individuals {
		merged = merged || ind.Name == "毛肚串" && len(ind.Facts["原料"]) == 1
	}
	if !merged {
		t.Errorf("the company's individuals: %+v", model.Ontology.Individuals)
	}
	lines := model.Plants[0].Lines
	if fz := lines[0].Stations[0]; fz.ID != "FZ-101" || fz.Machine != "freezer" || strings.Join(fz.Classes, ",") != "冷藏设备" {
		t.Errorf("the freezer station: %+v", fz)
	}
	if p := lines[1].Product; p.Key != "pot" || p.Individual != "牛油锅底" || strings.Join(p.Classes, ",") != "锅底" {
		t.Errorf("the pot: %+v", p)
	}
}
