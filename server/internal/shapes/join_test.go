package shapes

import (
	"encoding/json"
	"strings"
	"testing"
)

// Parts that do not touch are refused: a crankshaft with gaps between its journals fails, joined it passes.
func TestJoinedParts(t *testing.T) {
	spec := Spec{Target: Product, Envelope: ProductMax}
	gappy := `
shape.group('crank', {position: {y: 0.06}, rotation: {z: 90}}, () => {
  for (let i = 0; i < 4; i++) shape.cylinder({r: 0.03, h: 0.08}, {y: -0.21 + i * 0.14}, {name: 'journal' + i, color: 'metal'});
});`
	res, _ := run(t, spec, gappy)
	rep := res.Report()
	if res.OK() || !strings.Contains(rep, "floats apart") || !strings.Contains(rep, "parts must touch within 0.008 m") {
		t.Fatalf("%s", rep)
	}
	var cats map[string]int
	spec.OnCheck = func(c map[string]int) { cats = c }
	run(t, spec, gappy)
	if cats["gap"] == 0 {
		t.Fatalf("categories %v", cats)
	}
	res, _ = run(t, spec, `
shape.group('crank', {position: {y: 0.06}, rotation: {z: 90}}, () => {
  for (let i = 0; i < 4; i++) shape.cylinder({r: 0.03, h: 0.08}, {y: -0.21 + i * 0.14}, {name: 'journal' + i, color: 'metal'});
  shape.cylinder({r: 0.02, h: 0.5}, {}, {name: 'shaft', color: 'metal'});
});`)
	if !res.OK() {
		t.Fatalf("joined should pass:\n%s", res.Report())
	}

	// a machine's parts that rest on the floor are joined to it; a floating one is not
	res, _ = run(t, press, `
shape.box({w: 1, h: 0.6, d: 1}, {x: -0.8, y: 0.3, z: -0.6}, {name: 'left'});
shape.box({w: 1, h: 0.6, d: 1}, {x: 0.8, y: 0.3, z: -0.6}, {name: 'right'});
shape.box({w: 0.4, h: 0.4, d: 0.4}, {x: 0, y: 2.5, z: -0.6}, {name: 'lamp'});`)
	rep = res.Report()
	if res.OK() || !strings.Contains(rep, `"lamp": floats apart`) || strings.Contains(rep, `"left": floats`) {
		t.Fatalf("%s", rep)
	}
}

// A product far off its real size is refused with the size it should have; one near it passes.
func TestRealSize(t *testing.T) {
	rod := func(h string) string {
		return `shape.group('rod', {rotation: {z: 90}, position: {y: 0.03}}, () => {
  shape.cylinder({r: 0.03, h: ` + h + `}, {}, {name: 'beam', color: 'metal'});
});`
	}
	real, from := RealSize(&[3]float64{0.2, 0.07, 0.04}, 0.62, "C70S6")
	if from != RealGiven || real[0] != 0.2 {
		t.Fatalf("%v %s", real, from)
	}
	spec := Spec{Target: Product, Envelope: ProductMax, Name: "连杆", Real: real, RealFrom: from}
	res, _ := run(t, spec, rod("0.65"))
	rep := res.Report()
	if res.OK() || !strings.Contains(rep, "longest side of the 连杆 is 0.65 m") || !strings.Contains(rep, "accepted 0.111 to 0.36 m") || !strings.Contains(rep, "Scale the whole drawing by about 0.31") {
		t.Fatalf("%s", rep)
	}
	if res, _ = run(t, spec, rod("0.2")); !res.OK() {
		t.Fatalf("%s", res.Report())
	}
	if res, _ = run(t, spec, rod("0.05")); res.OK() {
		t.Fatalf("too small should fail:\n%s", res.Report())
	}
	// no size in the data: worked out from the weight (a 0.62 kg steel part is a hand long or less), refused far off
	est, from := RealSize(nil, 0.62, "C70S6")
	if from != RealEstimated || est[0] < 0.1 || est[0] > 0.2 {
		t.Fatalf("%v %s", est, from)
	}
	spec.Real, spec.RealFrom = est, from
	if res, _ = run(t, spec, rod("0.65")); res.OK() {
		t.Fatalf("%s", res.Report())
	}
	if res, _ = run(t, spec, rod("0.25")); !res.OK() {
		t.Fatalf("%s", res.Report())
	}
	// nothing known: no check, but the docs say to use the real-world size
	if _, from = RealSize(nil, 0, ""); from != "" {
		t.Fatal(from)
	}
	if d := docs(Spec{Target: Product, Envelope: ProductMax}); !strings.Contains(d, "Nothing in the data says how big it is") {
		t.Fatal(d)
	}
	if d := docs(spec); !strings.Contains(d, "From its weight") {
		t.Fatal(d)
	}
}

// Add-on mode: the old parts count and may be touched, only the new ones are checked and returned.
func TestAppend(t *testing.T) {
	old := []Part{{Shape: "box", Size: []float64{1, 0.6, 1}, At: [3]float64{-0.8, 0, -0.6}, Color: "blue"}}
	spec := press
	spec.Append, spec.Existing, spec.Body = true, old, "frame"
	if d := docs(spec); !strings.Contains(d, "ADD-ON MODE") || !strings.Contains(d, "box [1 0.6 1]") || strings.Contains(d, "Use most of the envelope") {
		t.Fatal(d)
	}
	res, d := run(t, spec, `shape.box({w: 0.4, h: 1, d: 0.4}, {x: -0.8, y: 1.1, z: -0.7}, {name: 'chimney'});`)
	if !res.OK() || len(d.Parts) != 1 || !strings.Contains(res.Report(), "1 new + 1 existing = 2 of at most 48") {
		t.Fatalf("%s", res.Report())
	}
	res, _ = run(t, spec, `shape.box({w: 0.4, h: 1, d: 0.4}, {x: -0.8, y: 2.1, z: -0.7}, {name: 'chimney'});`)
	if res.OK() || !strings.Contains(res.Report(), `"chimney": floats apart`) {
		t.Fatalf("%s", res.Report())
	}
	// the old parts count towards the limit
	spec.MaxParts = 3
	res, _ = run(t, spec, `for (let i = 0; i < 3; i++) shape.box({w: 0.2, h: 0.2, d: 0.2}, {x: -0.8 + i * 0.1, y: 0.7, z: -0.6});`)
	if res.OK() || !strings.Contains(res.Report(), "4 parts (1 of them already there); the limit is 3") {
		t.Fatalf("%s", res.Report())
	}
	// a built-in body is not known: one add-on group may sit anywhere on it, two may not
	spec.MaxParts = 0
	spec.Existing, spec.Body = nil, "press"
	if res, _ = run(t, spec, `shape.box({w: 0.4, h: 1, d: 0.4}, {x: 0, y: 2.1, z: -0.6}, {name: 'chimney'});`); !res.OK() {
		t.Fatalf("%s", res.Report())
	}
	res, _ = run(t, spec, `
shape.box({w: 0.4, h: 1, d: 0.4}, {x: 0, y: 2.1, z: -0.6}, {name: 'chimney'});
shape.box({w: 0.2, h: 0.2, d: 0.2}, {x: 0.8, y: 3.1, z: -0.6}, {name: 'lamp'});`)
	if res.OK() {
		t.Fatalf("%s", res.Report())
	}
}

func TestPackSpecs(t *testing.T) {
	raw := json.RawMessage(`{"flow":"belt","products":{"rod":{"name":"连杆","weight":0.62,"material":"C70S6","size":[0.2,0.07,0.04],
"stages":{"forged":{"parts":[{"shape":"box","size":[0.2,0.04,0.07],"at":[0,0,0],"rotate":[0,0,0],"color":"metal"}]}}}},
"machines":{"press":{"label":"压机","look":{"archetype":"press","parts":[{"shape":"box","size":[1,1,1],"at":[0,0,0],"rotate":[0,0,0],"color":"blue"}]}}}}`)
	spec, _, _, err := ProductSpec(raw, "rod", "forged", true)
	if err != nil || spec.RealFrom != RealGiven || spec.Real[0] != 0.2 || !spec.Append || len(spec.Existing) != 1 || spec.Name != "连杆" {
		t.Fatalf("%+v %v", spec, err)
	}
	spec, _, _, _ = ProductSpec(raw, "rod", "forged", false)
	if spec.Append || len(spec.Existing) != 0 {
		t.Fatalf("%+v", spec)
	}
	m, _, err := MachineSpec(raw, "press", true)
	if err != nil || !m.Append || m.Body != "press" || len(m.Existing) != 1 {
		t.Fatalf("%+v %v", m, err)
	}
}
