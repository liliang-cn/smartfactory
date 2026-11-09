package shapes

import (
	"context"
	"encoding/json"
	"math"
	"math/rand"
	"strings"
	"testing"

	"github.com/liliang-cn/agent-go/v3/pkg/codemode"

	"smartfactory/internal/models"
)

var press = Spec{Target: Machine, Envelope: [3]float64{2.4, 1.8, 4.4}, BeltY: 0.9, BeltW: 0.9, Clear: 0.6}

func run(t *testing.T, spec Spec, code string) (*codemode.Result, *Drawn) {
	t.Helper()
	res := codemode.Run(context.Background(), Mode(spec), code)
	if res.Error != nil {
		t.Fatalf("program error: %+v\n%s", res.Error, res.Report())
	}
	var d Drawn
	if err := json.Unmarshal(res.Artifact, &d); err != nil {
		t.Fatal(err)
	}
	return res, &d
}

func near(a, b [3]float64) bool {
	for k := range 3 {
		if math.Abs(a[k]-b[k]) > 1e-3 {
			return false
		}
	}
	return true
}

// A group's position and rotation end up in each part's `at` and `rotate`, and `at` is the
// bottom-face centre of the rotated part, the way parts.ts places it.
func TestFlattenGroupTransforms(t *testing.T) {
	_, d := run(t, press, `
shape.group('arm', {position: {x: 1, y: 0.5}, rotation: {y: 90}}, () => {
  shape.box({w: 2, h: 0.5, d: 0.4}, {x: 0.5}, {name: 'beam'});
});
shape.group('shaft', {position: {y: 0.3}, rotation: {x: 90}}, () => {
  shape.cylinder({r: 0.3, h: 1}, {}, {name: 'roll', rotation: {z: 90}, color: 'metal'});
});
shape.cylinder({r: 0.5, h: 0.15}, {x: -1, y: 1.2, z: -0.6}, {name: 'flywheel', rotation: {x: 90}, color: 'blueDeep', anim: {type: 'spin', axis: 'y', speed: 4}});
`)
	if len(d.Parts) != 3 {
		t.Fatalf("parts: %d", len(d.Parts))
	}
	beam, roll, fly := d.Parts[0], d.Parts[1], d.Parts[2]
	// the beam's centre (0.5, 0, 0) turns to (0, 0, -0.5) about y: centre (1, 0.5, -0.5), half height 0.25
	if !near(beam.At, [3]float64{1, 0.25, -0.5}) || !near(beam.Rotate, [3]float64{0, 90, 0}) {
		t.Fatalf("beam at %v rotate %v", beam.At, beam.Rotate)
	}
	min, max := PartBox(beam)
	if !near(min, [3]float64{0.8, 0.25, -1.5}) || !near(max, [3]float64{1.2, 0.75, 0.5}) {
		t.Fatalf("beam box %v %v", min, max)
	}
	// Rx(90)·Rz(90) is Euler XYZ (90, 0, 90): the roll lies along x, its bottom on the floor
	if !near(roll.Rotate, [3]float64{90, 0, 90}) || !near(roll.At, [3]float64{0, 0, 0}) {
		t.Fatalf("roll at %v rotate %v", roll.At, roll.Rotate)
	}
	if min, max := PartBox(roll); !near(min, [3]float64{-0.5, 0, -0.3}) || !near(max, [3]float64{0.5, 0.6, 0.3}) {
		t.Fatalf("roll box %v %v", min, max)
	}
	want := `{"shape":"cylinder","size":[0.5,0.15],"at":[-1,0.7,-0.6],"rotate":[90,0,0],"color":"blueDeep","anim":{"axis":"y","speed":4,"type":"spin"}}`
	if got, _ := json.Marshal(fly); string(got) != want {
		t.Fatalf("flywheel\n got %s\nwant %s", got, want)
	}
}

// angles inverts euler for any rotation, so a part keeps its orientation through the flattening.
func TestEulerRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for range 500 {
		a := [3]float64{rng.Float64()*360 - 180, rng.Float64()*180 - 90, rng.Float64()*360 - 180}
		m := euler(a)
		back := euler(angles(m))
		for i := range 3 {
			for j := range 3 {
				if math.Abs(m[i][j]-back[i][j]) > 1e-9 {
					t.Fatalf("%v -> %v", a, angles(m))
				}
			}
		}
	}
}

func TestMachineChecks(t *testing.T) {
	res, _ := run(t, press, `
shape.box({w: 2.2, h: 0.5, d: 1.6}, {y: -0.1}, {name: 'bed'});
shape.box({w: 0.3, h: 4.6, d: 0.3}, {x: 1.2, y: 2.3, z: -0.6}, {name: 'column'});
shape.box({w: 0.4, h: 0.3, d: 0.4}, {y: 1.0}, {name: 'die'});
shape.box({w: 0.1, h: 0.1, d: 0.1}, {x: 1.2, y: 2.3, z: -0.6}, {name: 'bolt'});
`)
	rep := res.Report()
	for _, want := range []string{
		`"bed": reaches below the floor`,
		`"column": sticks out of the envelope along x by 0.15, y (top 4.6) by 0.2 m`,
		`"die": blocks the belt corridor`,
		`"bolt": is buried inside "column"`,
		"belt corridor (keep clear): |z| < 0.45, y 0.85 to 1.5",
	} {
		if !strings.Contains(rep, want) {
			t.Fatalf("report missing %q:\n%s", want, rep)
		}
	}
	if res.OK() {
		t.Fatal("should fail")
	}

	// the same press done right passes: bed below the belt, crown above, columns beside it
	res, d := run(t, press, `
shape.box({w: 2.2, h: 0.6, d: 1.6}, {y: 0.3}, {name: 'bed'});
for (const x of [-0.95, 0.95]) shape.box({w: 0.3, h: 3.2, d: 0.4}, {x, y: 0.6 + 1.6, z: -0.65}, {name: 'column'});
shape.box({w: 2.4, h: 0.6, d: 1.4}, {y: 4.1, z: -0.2}, {name: 'crown', color: 'blue'});
shape.box({w: 0.8, h: 0.5, d: 0.8}, {y: 1.9}, {name: 'ram', color: 'metal', anim: {type: 'stroke', axis: 'y', to: -0.4}});
shape.box({w: 0.2, h: 1.65, d: 0.2}, {y: 2.975}, {name: 'rod', color: 'metal'});
`)
	if !res.OK() {
		t.Fatalf("should pass:\n%s", res.Report())
	}
	if d.Size != press.Envelope || len(d.Parts) != 6 {
		t.Fatalf("size %v parts %d", d.Size, len(d.Parts))
	}

	// too many parts for a plant full of them
	res, _ = run(t, press, `for (let i = 0; i < 60; i++) shape.box({w: 0.03, h: 0.03, d: 0.03}, {x: -1.1 + i * 0.035, y: 3, z: -0.8});`)
	if !strings.Contains(res.Report(), "60 parts; the limit is 48") {
		t.Fatalf("%s", res.Report())
	}
}

func TestProductChecks(t *testing.T) {
	spec := Spec{Target: Product, Envelope: ProductMax}
	res, d := run(t, spec, `
shape.group('crank', {position: {y: 0.15}, rotation: {z: 90}}, () => {
  shape.cylinder({r: 0.035, h: 0.62}, {}, {name: 'journal', color: 'metal'});
  for (let i = 0; i < 4; i++) shape.box({w: 0.2, h: 0.04, d: 0.08}, {y: -0.21 + i * 0.14, x: (i % 2 ? 1 : -1) * 0.05}, {name: 'web', color: 'metal'});
});`)
	if !res.OK() {
		t.Fatalf("%s", res.Report())
	}
	// lying along x: 0.62 long (the journal), 0.08 deep and 0.3 high (the webs, offset up and down)
	if d.Size != [3]float64{0.62, 0.08, 0.3} || d.Min[1] != 0 {
		t.Fatalf("size %v min %v", d.Size, d.Min)
	}
	res, _ = run(t, spec, `shape.box({w: 7, h: 0.5, d: 1}, {x: 2, y: 0.25});`)
	rep := res.Report()
	if res.OK() || !strings.Contains(rep, "7 m along x; the limit is 6 m") || !strings.Contains(rep, "centre it on x = 0") {
		t.Fatalf("%s", rep)
	}
	var cats map[string]int
	spec.OnCheck = func(c map[string]int) { cats = c }
	run(t, spec, `for (let i = 0; i < 22; i++) shape.sphere({r: 0.01}, {x: i * 0.03 - 0.3, y: 0.01});`)
	if cats["count"] != 1 {
		t.Fatalf("categories %v", cats)
	}
}

func TestProgramMistakesReportLines(t *testing.T) {
	res := codemode.Run(context.Background(), Mode(press), "shape.box({w: 1, h: 1, d: 1}, {y: 0.5});\nshape.cube({w: 1});")
	if res.Error == nil || res.Error.Line != 2 || !strings.Contains(res.Error.Message, "shape.cube does not exist") {
		t.Fatalf("%+v", res.Error)
	}
	res = codemode.Run(context.Background(), Mode(press), "shape.box({w: 1, h: 1, d: 1}, {y: 0.5}, {color: 'purple'});")
	if res.Error == nil || !strings.Contains(res.Error.Message, "palette name") && !strings.Contains(res.Error.Message, "one of white") {
		t.Fatalf("%+v", res.Error)
	}
}

// A ready-made model goes in at its real size as a box for every check, keeps its id, and can be
// grown evenly; a model the drawer has not found, or a server without a catalogue, is refused.
func TestModelParts(t *testing.T) {
	tents := func(id string) (models.Info, bool) {
		if id == "dome-tent-9ee59d" {
			return models.Info{ID: id, Title: "Dome Tent", Size: [3]float64{3, 1.31, 3}}, true
		}
		return models.Info{}, false
	}
	pitch := Spec{Target: Machine, Envelope: [3]float64{5, 5, 2.4}, BeltY: 0.9, BeltW: 0.9, Clear: 0.6, Models: tents}
	res, d := run(t, pitch, `
const s = shape.modelSize('dome-tent-9ee59d');
shape.box({w: 4.6, h: 0.1, d: 1.6}, {y: 0.05, z: -1.6}, {name: 'deck', color: 'wood'});
shape.model('dome-tent-9ee59d', {y: 0.1 + s.h * 0.5 / 2, z: -1.6}, {name: 'tent', rotation: {y: 90}, scale: 0.5});
`)
	if !res.OK() {
		t.Fatalf("a tent on its deck fails:\n%s", res.Report())
	}
	tent := d.Parts[1]
	if tent.Shape != "model" || tent.Model != "dome-tent-9ee59d" || !near([3]float64(tent.Size), [3]float64{1.5, 0.655, 1.5}) {
		t.Fatalf("tent part: %+v", tent)
	}
	min, max := PartBox(tent)
	// half the size, standing on the deck
	if !near([3]float64{min[1], max[1], max[0] - min[0]}, [3]float64{0.1, 0.755, 1.5}) {
		t.Fatalf("tent box %v %v", min, max)
	}
	b, _ := json.Marshal(tent)
	if !strings.Contains(string(b), `"model":"dome-tent-9ee59d"`) || strings.Contains(string(b), "round") {
		t.Fatalf("as the twin reads it: %s", b)
	}

	// a chair inside the tent's box is not buried: the box is mostly air
	res, _ = run(t, pitch, `
shape.model('dome-tent-9ee59d', {y: 0.655}, {name: 'tent'});
shape.box({w: 0.4, h: 0.4, d: 0.4}, {y: 0.2}, {name: 'stool'});
`)
	for _, is := range res.Issues {
		if strings.Contains(is.Message, "buried") {
			t.Fatalf("a part inside a model is called buried: %s", is.Message)
		}
	}

	for _, c := range []struct {
		spec       Spec
		code, want string
	}{
		{pitch, `shape.model('camp-chair-1', {y: 0.4});`, "call search_models"},
		{pitch, `shape.model('dome-tent-9ee59d', {y: 0.7}, {scale: 9});`, "scale must be between"},
		{pitch, `shape.model('dome-tent-9ee59d', {y: 0.7}, {color: 'red'});`, "unknown option 'color'"},
		{press, `shape.model('dome-tent-9ee59d', {y: 0.7});`, "no model catalogue"},
	} {
		res := codemode.Run(context.Background(), Mode(c.spec), c.code)
		if res.Error == nil || !strings.Contains(res.Error.Message, c.want) {
			t.Errorf("%s: want %q, got %+v", c.code, c.want, res.Error)
		}
	}
	if strings.Contains(Mode(press).Docs, "search_models") || !strings.Contains(Mode(pitch).Docs, "shape.model(id") {
		t.Error("the docs tell of models only when there is a catalogue")
	}
}
