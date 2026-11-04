// Package shapes is SmartFactory's code mode for machine and product shapes: the model writes
// a small JavaScript program against a builder (boxes, cylinders, cones, spheres, tori,
// capsules, nested groups), the program runs in agent-go's codemode sandbox, and the node tree
// it builds is flattened into the twin's parts (web/src/config/schema.ts, Part). The checks are
// the twin's: a machine fits its envelope, stands on the floor and keeps the belt clear; a
// product has a sane size; neither has more parts than a plant full of them can draw.
//
// Frames. The builder speaks three.js: metres, y up, a part's position is its centre, rotations
// in degrees applied in X, Y, Z order, groups carry a position and rotation for their children.
// A Part's `at` is the centre of the bottom of its bounding box after `rotate` (parts.ts), in
// the machine's frame (belt along +x at z = 0, aisle towards +z) or the product's (bottom
// centre of the unit).
package shapes

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/liliang-cn/agent-go/v3/pkg/codemode"
	"github.com/liliang-cn/agent-go/v3/pkg/codemode/scene"
)

//go:embed shape-api.js
var prelude string

// Part is one primitive of a machine's look or a product stage, as the twin reads it.
type Part struct {
	Shape   string         `json:"shape"`
	Size    []float64      `json:"size"`
	At      [3]float64     `json:"at"`
	Rotate  [3]float64     `json:"rotate"`
	Color   string         `json:"color"`
	Metal   *float64       `json:"metal,omitempty"`
	Rough   *float64       `json:"rough,omitempty"`
	Glow    string         `json:"glow,omitempty"`
	Opacity *float64       `json:"opacity,omitempty"`
	Round   *float64       `json:"round,omitempty"`
	Anim    map[string]any `json:"anim,omitempty"`
}

// Target is what a shape is for.
type Target string

const (
	Machine Target = "machine"
	Product Target = "product"
)

// Limits on parts: every part is a draw call, on every machine of a plant or every unit on a line.
const (
	MaxMachineParts = 48
	MaxProductParts = 20
)

// Spec is what a shape must fit. Sizes are [length along x, depth along z, height along y].
type Spec struct {
	Target Target
	// Envelope a machine must fit in (and a product must not exceed), [L, D, H] in metres.
	Envelope [3]float64
	// Belt top, belt width and the clear height above the belt a machine must keep free.
	BeltY, BeltW, Clear float64
	// MaxParts defaults to MaxMachineParts or MaxProductParts.
	MaxParts int
	// Name is what a product is called (for the messages).
	Name string
	// Real is how big one unit of the product really is, [l, d, h] in metres, and RealFrom where
	// that comes from (RealGiven: the pack says so; RealEstimated: worked out from its weight).
	// A drawing whose longest side is far off it is refused. Zero when nothing is known.
	Real     [3]float64
	RealFrom string
	// Append: the drawing is added to what is already there (Existing, the look's or the stage's
	// parts; Body the machine's archetype, whose own geometry is not known here) instead of
	// replacing it. Only the new parts are drawn and checked against the envelope; the old ones
	// count towards the part limit and the size, and new parts may touch them.
	Append   bool
	Existing []Part
	Body     string
	// OnCheck, when set, is told the error categories of every checked attempt
	// (overlap, envelope, belt, floor, count, size, empty), for progress lines.
	OnCheck func(errors map[string]int)
}

func (s Spec) maxParts() int {
	if s.MaxParts > 0 {
		return s.MaxParts
	}
	if s.Target == Product {
		return MaxProductParts
	}
	return MaxMachineParts
}

// Where a product's real size comes from.
const (
	RealGiven     = "given"
	RealEstimated = "estimated"
)

// How far off its real longest side a product may be drawn (times, either way).
const (
	realGivenFactor     = 1.8
	realEstimatedFactor = 2.2
)

// How close parts must be to count as joined, in metres.
const (
	joinMachine = 0.02
	joinProduct = 0.008
)

// DefaultSize is a machine's [length, depth, height] when its look gives none (core/layout.ts).
var DefaultSize = map[string][3]float64{
	"enclosure": {2.6, 2.3, 2.45}, "press": {2.4, 1.8, 4.4}, "robot": {3.8, 3.6, 2.8}, "tunnel": {4.5, 1.9, 2.0},
	"gantry": {1.6, 1.6, 2.3}, "packer": {2.3, 2.1, 2.2}, "manual": {2.9, 3.2, 2.6}, "safety": {1.9, 1.7, 2.3},
	"chamber": {3, 1.8, 2.1}, "filler": {2.6, 2.6, 2.4}, "booth": {3.4, 2.0, 2.4}, "tank": {2.6, 3.4, 3.2},
	"roller": {2.4, 2.2, 2.6}, "frame": {2, 2, 2},
}

// ProductMax is the largest product a line takes, [l, d, h].
var ProductMax = [3]float64{6, 3, 3}

// Drawn is the artifact: the parts, their bounds, and for a machine the look size to give it.
type Drawn struct {
	Parts []Part     `json:"parts"`
	Min   [3]float64 `json:"min"`
	Max   [3]float64 `json:"max"`
	// Size is look.size for a machine ([L, D, H], the envelope it was drawn in); for a product
	// the unit's [l, d, h].
	Size [3]float64 `json:"size"`
}

// Mode is the code mode for spec. Its artifact (after Finalize) is a Drawn.
func Mode(spec Spec) *codemode.Mode {
	if spec.Target != Product {
		spec.Target = Machine
	}
	fr := map[string]any{"L": spec.Envelope[0], "D": spec.Envelope[1], "H": spec.Envelope[2]}
	if spec.Target == Machine {
		fr["beltY"], fr["beltW"], fr["clear"] = spec.BeltY, spec.BeltW, spec.Clear
	}
	m := &codemode.Mode{
		Name:    "sfshape",
		Tool:    "build_shape",
		Summary: "Build the 3D shape of a factory " + string(spec.Target) + " for SmartFactory's digital twin by writing a JavaScript program against a small shape builder. The result becomes the twin's parts.",
		Docs:    docs(spec),
		Example: example(spec.Target),
		Prelude: prelude,
		Export:  "__shape.export()",
		Globals: map[string]any{"__shapeMaxNodes": spec.maxParts()*4 + 20, "__frame": fr},
		Validators: []codemode.Validator{func(_ context.Context, raw json.RawMessage) codemode.Check {
			return Validate(raw, spec)
		}},
		Finalize: func(raw json.RawMessage) (json.RawMessage, error) {
			d, err := Convert(raw, spec)
			if err != nil {
				return nil, err
			}
			return json.Marshal(d)
		},
	}
	return m
}

func docs(s Spec) string { return baseDocs(s) + sizeDocs(s) + appendDocs(s) + joinDocs(s) }

// sizeDocs tells a product's real size.
func sizeDocs(s Spec) string {
	if s.Target != Product {
		return ""
	}
	name := s.Name
	if name == "" {
		name = "this product"
	}
	long := longest(s.Real)
	switch s.RealFrom {
	case RealGiven:
		lo, hi := long/realGivenFactor, long*realGivenFactor
		return fmt.Sprintf(`

REAL SIZE. One %s really measures about %s x %s x %s m (length x depth x height, its longest side %s m). Draw it at that
real size, not bigger: the report errors when the longest side of the drawing is outside %s to %s m.`,
			name, fnum(s.Real[0]), fnum(s.Real[1]), fnum(s.Real[2]), fnum(long), fnum(lo), fnum(hi))
	case RealEstimated:
		lo, hi := long/realEstimatedFactor, long*realEstimatedFactor
		return fmt.Sprintf(`

REAL SIZE. From its weight, one %s is roughly %s m long on its longest side (a compact body about %s x %s x %s m). Draw it
at a real-world size, not bigger: the report errors when the longest side of the drawing is outside %s to %s m.
If you know better what it really measures, use that, within those limits.`,
			name, fnum(long), fnum(s.Real[0]), fnum(s.Real[1]), fnum(s.Real[2]), fnum(lo), fnum(hi))
	}
	return `

REAL SIZE. Nothing in the data says how big it is. Draw it at the size such a thing really has in the real world
(a connecting rod is about 0.2 m, a crankshaft about 0.5 m, a bottle 0.2 m, a carton 0.4 m), not enlarged to fill the frame,
and say in your final answer which size you took.`
}

// appendDocs tells what is already there when the drawing is added to it.
func appendDocs(s Spec) string {
	if !s.Append {
		return ""
	}
	what := "machine"
	if s.Target == Product {
		what = "product stage"
	}
	var b strings.Builder
	fmt.Fprintf(&b, `

ADD-ON MODE. This %s is NOT drawn from scratch: its present look stays exactly as it is, and the parts you build are ADDED to it.
Build only the new parts (what the description asks to add), placed where they belong on the existing look. Do not redraw the
existing look. Existing parts count towards the limit of %d parts (%d are there, so you have %d left) and towards the size.`,
		what, s.maxParts(), len(s.Existing), max(0, s.maxParts()-len(s.Existing)))
	if len(s.Existing) > 0 {
		b.WriteString("\nThe existing extra parts (shape size at-bottom-centre rotate colour), in metres and degrees:\n")
		for i, p := range s.Existing {
			if i == 30 {
				fmt.Fprintf(&b, "  ... %d more\n", len(s.Existing)-30)
				break
			}
			fmt.Fprintf(&b, "  %s %v at %s rotate %s %s\n", p.Shape, p.Size, fv(p.At), fv(p.Rotate), p.Color)
		}
	}
	if s.Target == Machine {
		body := "a plain base"
		if s.Body != "" && s.Body != "frame" {
			body = fmt.Sprintf("a built-in body of the kind %q that fills the envelope's footprint (its geometry is not shown to you)", s.Body)
		}
		fmt.Fprintf(&b, `
Under all of it the machine has %s. Attach the new parts to the body or to the existing parts, on the outside of it, and keep them inside the envelope and clear of the belt corridor.`, body)
	}
	return b.String()
}

// joinDocs asks for parts that are joined up.
func joinDocs(s Spec) string {
	tol := joinMachine
	if s.Target == Product {
		tol = joinProduct
	}
	note := " A part resting on the floor counts as joined to the floor."
	if s.Target == Product {
		note = " The product is one piece: it has no floor, so every part must touch the others."
	}
	return fmt.Sprintf(`

JOINED PARTS. Parts must form one connected whole: the bounding box of every part has to touch, or slightly overlap, the box of
another part (a gap of more than %s m is an error). Never leave a gap between neighbours, such as the journals and webs of
a shaft or a column and the crown above it: make them meet, or overlap by a few millimetres, or bridge the gap with a part.%s`, fnum(tol), note)
}

func longest(v [3]float64) float64 { return math.Max(v[0], math.Max(v[1], v[2])) }

func baseDocs(s Spec) string {
	common := `Units are metres, y is up, the floor is y = 0. A part's position is its CENTRE, so a box of
height h standing on the floor has y = h/2. Rotations are degrees, XYZ order. Positions and rotations
are {x, y, z} (missing parts are 0) or [x, y, z].

shape.box({w, h, d}, position, opts)        w along x, h along y, d along z
shape.cylinder({r, h}, position, opts)      axis along y (rotate {x: 90} to point it along z, {z: 90} along x)
shape.cone({r, h}, position, opts)          axis along y, tip up
shape.sphere({r}, position, opts)
shape.torus({r, tube}, position, opts)      ring in the XY plane (rotate {x: 90} to lay it flat)
shape.capsule({r, len}, position, opts)     axis along y, total height len + 2r
  opts: {name, color, rotation, metal, rough, glow, opacity, round, anim}
    color: '#rrggbb' or a palette name: white, shell, blue, blueDeep, blueSoft, yellow, orange, kraft,
           wood, grey, greyDark, ink, metal, glass, red, green, mint (use these for the factory look)
    metal, rough, opacity: 0..1; glow: an emissive colour; round: corner radius of a box
    anim: {type: 'spin', axis: 'x'|'y'|'z', speed} spins the part about its own axis while working;
          {type: 'stroke', axis, to, window: [0.2, 0.8]} moves it by 'to' metres during the cycle
shape.group(name, fn) / shape.group(name, {position, rotation}, fn)   the transform applies inside fn
shape.count() -> nodes so far
frame: the numbers below, e.g. frame.L

Give every part a short name; the report uses them.
`
	if s.Target == Product {
		return common + fmt.Sprintf(`
This is a PRODUCT (one unit as it travels down the line). Its origin is the centre of the unit's
bottom: centre it on x = 0, z = 0 and let it rest on y = 0. x is its length along the line.
It may be at most %s m long (x), %s m deep (z) and %s m high (y): frame = {L: %s, D: %s, H: %s}.
At most %d parts: every unit on every line draws them all, so a few well-chosen parts beat detail.
The report errors on parts that are buried inside another part (they are never seen).`,
			fnum(s.Envelope[0]), fnum(s.Envelope[1]), fnum(s.Envelope[2]), fnum(s.Envelope[0]), fnum(s.Envelope[1]), fnum(s.Envelope[2]), s.maxParts())
	}
	fill := " Use most of the envelope so the machine reads at its real size."
	if s.Append {
		fill = ""
	}
	return common + fmt.Sprintf(`
This is a MACHINE. Its origin is the centre of its footprint on the floor. It must fit its envelope:
x in [-L/2, L/2], z in [-D/2, D/2], y in [0, H], with frame = {L: %s, D: %s, H: %s, beltY: %s, beltW: %s, clear: %s}.
The production line's belt runs along x straight through the machine at z = 0: belt top at y = beltY,
width beltW. Keep the belt corridor clear: no part may be inside |z| < beltW/2 between
y = beltY - 0.05 and y = beltY + clear (the products ride there). Tools, rams and heads hang above it;
beds and bases go below it or beside it. The operator aisle is towards +z: put motors, flywheels and
cabinets at the back (-z) or the ends. A base plate, a control panel and a signal lamp are added for you.
At most %d parts: a plant has dozens of machines and every part is drawn on each, so build the
outline from a few big parts.%s
The report errors on parts that are buried inside another part (they are never seen).`,
		fnum(s.Envelope[0]), fnum(s.Envelope[1]), fnum(s.Envelope[2]), fnum(s.BeltY), fnum(s.BeltW), fnum(s.Clear), s.maxParts(), fill)
}

func example(t Target) string {
	if t == Product {
		return `// a flanged shaft lying along x
shape.group('shaft', {position: {y: 0.06}, rotation: {z: 90}}, () => {
  shape.cylinder({r: 0.04, h: 0.6}, {}, {name: 'body', color: 'metal'});
  shape.cylinder({r: 0.06, h: 0.03}, {y: 0.28}, {name: 'flange', color: 'metal'});
});`
	}
	return `// a column drill beside the belt
const back = -frame.D / 2 + 0.3;
shape.box({w: 0.8, h: 0.5, d: 0.5}, {y: 0.25, z: back}, {name: 'base', color: 'blueDeep'});
shape.cylinder({r: 0.08, h: frame.H - 0.6}, {y: 0.5 + (frame.H - 0.6) / 2, z: back}, {name: 'column', color: 'metal'});
const headY = frame.beltY + frame.clear + 0.3;
shape.group('head', {position: {y: headY}}, () => {
  shape.box({w: 0.5, h: 0.5, d: 0.9}, {z: back / 2}, {name: 'housing', color: 'white'});
  shape.cylinder({r: 0.04, h: 0.25}, {y: -0.37}, {name: 'spindle', color: 'metal', anim: {type: 'spin', axis: 'y', speed: 20}});
});`
}

// ---------- flattening ----------

type mat3 [3][3]float64

func ident() mat3 { return mat3{{1, 0, 0}, {0, 1, 0}, {0, 0, 1}} }

func (a mat3) mul(b mat3) mat3 {
	var r mat3
	for i := range 3 {
		for j := range 3 {
			for k := range 3 {
				r[i][j] += a[i][k] * b[k][j]
			}
		}
	}
	return r
}

func (a mat3) apply(v [3]float64) [3]float64 {
	var r [3]float64
	for i := range 3 {
		r[i] = a[i][0]*v[0] + a[i][1]*v[1] + a[i][2]*v[2]
	}
	return r
}

// euler is R = Rx·Ry·Rz (three.js order "XYZ"), angles in degrees.
func euler(deg [3]float64) mat3 {
	rad := math.Pi / 180
	cx, sx := math.Cos(deg[0]*rad), math.Sin(deg[0]*rad)
	cy, sy := math.Cos(deg[1]*rad), math.Sin(deg[1]*rad)
	cz, sz := math.Cos(deg[2]*rad), math.Sin(deg[2]*rad)
	rx := mat3{{1, 0, 0}, {0, cx, -sx}, {0, sx, cx}}
	ry := mat3{{cy, 0, sy}, {0, 1, 0}, {-sy, 0, cy}}
	rz := mat3{{cz, -sz, 0}, {sz, cz, 0}, {0, 0, 1}}
	return rx.mul(ry).mul(rz)
}

// angles is the inverse of euler (three.js Euler.setFromRotationMatrix, order "XYZ").
func angles(m mat3) [3]float64 {
	deg := 180 / math.Pi
	y := math.Asin(math.Max(-1, math.Min(1, m[0][2])))
	var x, z float64
	if math.Abs(m[0][2]) < 0.9999999 {
		x = math.Atan2(-m[1][2], m[2][2])
		z = math.Atan2(-m[0][1], m[0][0])
	} else {
		x = math.Atan2(m[2][1], m[1][1])
	}
	return [3]float64{x * deg, y * deg, z * deg}
}

// halfExtents of a primitive in its own frame (the three.js geometry's bounding box).
func halfExtents(shape string, size []float64) [3]float64 {
	at := func(i int, d float64) float64 {
		if i < len(size) {
			return size[i]
		}
		return d
	}
	a := at(0, 0)
	switch shape {
	case "box":
		b := at(1, a)
		return [3]float64{a / 2, b / 2, at(2, a) / 2}
	case "cylinder", "cone":
		return [3]float64{a, at(1, a) / 2, a}
	case "torus":
		t := at(1, 0.05)
		return [3]float64{a + t, a + t, t}
	case "capsule":
		return [3]float64{a, at(1, a)/2 + a, a}
	}
	return [3]float64{a, a, a}
}

// Placed is a part with where it ended up: its world bounding box and the program line.
type Placed struct {
	Part     Part
	Path     string
	Line     int
	Min, Max [3]float64
	// Old: a part that was already there (add-on mode), not drawn by this program.
	Old bool
}

// placeExisting is the parts already there, as the checks see them.
func placeExisting(parts []Part) []Placed {
	out := make([]Placed, 0, len(parts))
	for i, p := range parts {
		min, max := PartBox(p)
		out = append(out, Placed{Part: p, Path: fmt.Sprintf("existing %s #%d", p.Shape, i+1), Min: min, Max: max, Old: true})
	}
	return out
}

func (p Placed) volume() float64 {
	return (p.Max[0] - p.Min[0]) * (p.Max[1] - p.Min[1]) * (p.Max[2] - p.Min[2])
}

// box is a part's world bounding box from its centre and rotation.
func box(shape string, size []float64, c [3]float64, r mat3) (min, max [3]float64) {
	h := halfExtents(shape, size)
	for i := range 3 {
		e := math.Abs(r[i][0])*h[0] + math.Abs(r[i][1])*h[1] + math.Abs(r[i][2])*h[2]
		min[i], max[i] = c[i]-e, c[i]+e
	}
	return
}

// PartBox is the world bounding box of a part as the twin places it.
func PartBox(p Part) (min, max [3]float64) {
	r := euler(p.Rotate)
	h := halfExtents(p.Shape, p.Size)
	ey := math.Abs(r[1][0])*h[0] + math.Abs(r[1][1])*h[1] + math.Abs(r[1][2])*h[2]
	return box(p.Shape, p.Size, [3]float64{p.At[0], p.At[1] + ey, p.At[2]}, r)
}

// Flatten applies every group's transform and turns each mesh of a scene-mode node tree into a
// Part: its world rotation becomes `rotate`, and its centre becomes `at`, the bottom-face centre
// of the rotated bounding box.
func Flatten(nodes []*scene.Node) ([]Placed, error) {
	var out []Placed
	var walk func(nodes []*scene.Node, rot mat3, pos [3]float64, prefix string) error
	walk = func(nodes []*scene.Node, rot mat3, pos [3]float64, prefix string) error {
		for i, n := range nodes {
			if n == nil {
				continue
			}
			name := n.Name
			if name == "" {
				name = fmt.Sprintf("%s#%d", n.Type, i)
			}
			path := name
			if prefix != "" {
				path = prefix + "/" + name
			}
			if n.Scale != nil {
				return fmt.Errorf("%s: scale is not supported; give the size instead", path)
			}
			local := ident()
			if n.Rotation != nil {
				local = euler(*n.Rotation)
			}
			p := rot.apply(n.Position)
			wpos := [3]float64{pos[0] + p[0], pos[1] + p[1], pos[2] + p[2]}
			wrot := rot.mul(local)
			if n.Type == "group" {
				if err := walk(n.Children, wrot, wpos, path); err != nil {
					return err
				}
				continue
			}
			part, err := meshPart(n)
			if err != nil {
				return fmt.Errorf("%s: %w", path, err)
			}
			min, max := box(part.Shape, part.Size, wpos, wrot)
			part.Rotate = angles(wrot)
			part.At = [3]float64{wpos[0], min[1], wpos[2]}
			out = append(out, Placed{Part: part, Path: path, Line: n.Line, Min: min, Max: max})
		}
		return nil
	}
	return out, walk(nodes, ident(), [3]float64{}, "")
}

// meshPart reads a mesh node's shape, size and surface (its placement is set by Flatten).
func meshPart(n *scene.Node) (Part, error) {
	g := n.Geometry
	p := Part{Shape: n.Type}
	switch n.Type {
	case "box":
		p.Size = []float64{g["w"], g["h"], g["d"]}
	case "cylinder", "cone":
		p.Size = []float64{g["r"], g["h"]}
	case "sphere":
		p.Size = []float64{g["r"]}
	case "torus":
		p.Size = []float64{g["r"], g["tube"]}
	case "capsule":
		p.Size = []float64{g["r"], g["len"]}
	default:
		return p, fmt.Errorf("unknown shape %q", n.Type)
	}
	for _, v := range p.Size {
		if !(v > 0) {
			return p, fmt.Errorf("sizes must be greater than 0")
		}
	}
	var m struct {
		Color   string         `json:"color"`
		Metal   *float64       `json:"metal"`
		Rough   *float64       `json:"rough"`
		Glow    string         `json:"glow"`
		Opacity *float64       `json:"opacity"`
		Round   *float64       `json:"round"`
		Anim    map[string]any `json:"anim"`
	}
	if len(n.Material) > 0 {
		if err := json.Unmarshal(n.Material, &m); err != nil {
			return p, fmt.Errorf("surface: %w", err)
		}
	}
	p.Color = m.Color
	if p.Color == "" {
		p.Color = "white"
	}
	p.Metal, p.Rough, p.Glow, p.Opacity, p.Round, p.Anim = m.Metal, m.Rough, m.Glow, m.Opacity, m.Round, m.Anim
	return p, nil
}

func parse(raw json.RawMessage) ([]Placed, error) {
	s, err := scene.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("shape JSON does not parse: %w", err)
	}
	return Flatten(s.Nodes)
}

// Convert turns a program's node tree into the twin's parts, rounded to the millimetre.
func Convert(raw json.RawMessage, spec Spec) (*Drawn, error) {
	placed, err := parse(raw)
	if err != nil {
		return nil, err
	}
	d := &Drawn{Parts: make([]Part, 0, len(placed))}
	min, max := bounds(placed)
	d.Min, d.Max = roundV(min, 1e3), roundV(max, 1e3)
	for _, p := range placed {
		q := p.Part
		q.At = roundV(q.At, 1e3)
		q.Rotate = roundV(q.Rotate, 100)
		for i, v := range q.Size {
			q.Size[i] = math.Max(0.001, round(v, 1e3))
		}
		d.Parts = append(d.Parts, q)
	}
	if spec.Target == Product {
		umin, umax := bounds(append(slices.Clone(placed), placeExisting(spec.Existing)...))
		d.Size = roundV([3]float64{umax[0] - umin[0], umax[2] - umin[2], umax[1] - umin[1]}, 1e3)
	} else {
		d.Size = spec.Envelope
	}
	return d, nil
}

func round(v, k float64) float64 {
	r := math.Round(v*k) / k
	if r == 0 {
		return 0 // no -0 in the JSON
	}
	return r
}

func roundV(v [3]float64, k float64) [3]float64 {
	return [3]float64{round(v[0], k), round(v[1], k), round(v[2], k)}
}

func bounds(ps []Placed) (min, max [3]float64) {
	min = [3]float64{math.Inf(1), math.Inf(1), math.Inf(1)}
	max = [3]float64{math.Inf(-1), math.Inf(-1), math.Inf(-1)}
	for _, p := range ps {
		for k := range 3 {
			min[k] = math.Min(min[k], p.Min[k])
			max[k] = math.Max(max[k], p.Max[k])
		}
	}
	return
}

// ---------- checks ----------

// Validate checks a program's node tree against spec and measures it for the report.
func Validate(raw json.RawMessage, spec Spec) codemode.Check {
	var c codemode.Check
	cats := map[string]int{}
	errorf := func(cat string, line int, node, format string, a ...any) {
		cats[cat]++
		c.Issues = append(c.Issues, codemode.Issue{Severity: codemode.SeverityError, Line: line, Node: node, Message: fmt.Sprintf(format, a...)})
	}
	warnf := func(line int, node, format string, a ...any) {
		c.Issues = append(c.Issues, codemode.Issue{Severity: codemode.SeverityWarning, Line: line, Node: node, Message: fmt.Sprintf(format, a...)})
	}
	defer func() {
		if spec.OnCheck != nil {
			spec.OnCheck(cats)
		}
	}()
	ps, err := parse(raw)
	if err != nil {
		errorf("empty", 0, "", "%s", err)
		return c
	}
	if len(ps) == 0 {
		errorf("empty", 0, "", "nothing was built; call shape.box / shape.cylinder / …")
		c.Stats = append(c.Stats, codemode.Stat{Key: "parts", Value: "0"})
		return c
	}
	limit := spec.maxParts()
	all := append(slices.Clone(ps), placeExisting(spec.Existing)...)
	counts := map[string]int{}
	for _, p := range ps {
		counts[p.Part.Shape]++
	}
	kinds := make([]string, 0, len(counts))
	for k := range counts {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	var list []string
	for _, k := range kinds {
		list = append(list, fmt.Sprintf("%s %d", k, counts[k]))
	}
	min, max := bounds(all)
	size := [3]float64{max[0] - min[0], max[1] - min[1], max[2] - min[2]}
	partsNow := fmt.Sprintf("%d of at most %d (%s)", len(all), limit, strings.Join(list, ", "))
	if spec.Append {
		partsNow = fmt.Sprintf("%d new + %d existing = %d of at most %d (new: %s)", len(ps), len(all)-len(ps), len(all), limit, strings.Join(list, ", "))
	}
	c.Stats = append(c.Stats,
		codemode.Stat{Key: "parts", Value: partsNow},
		codemode.Stat{Key: "bounds", Value: fmt.Sprintf("min %s max %s, size %s m (x, y, z)", fv(min), fv(max), fv(size))},
	)
	if len(all) > limit {
		there := ""
		if old := len(all) - len(ps); old > 0 {
			there = fmt.Sprintf(" (%d of them already there)", old)
		}
		errorf("count", 0, "", "%d parts%s; the limit is %d. Merge small details into bigger parts or leave them out", len(all), there, limit)
	}

	below := 0
	for _, p := range ps {
		if p.Min[1] < -1e-3 {
			below++
			if below <= 4 {
				errorf("floor", p.Line, p.Path, "reaches below the floor (bottom at y = %s); position is the centre", fnum(p.Min[1]))
			}
		}
	}

	L, D, H := spec.Envelope[0], spec.Envelope[1], spec.Envelope[2]
	const tol = 0.005
	if spec.Target == Product {
		c.Stats = append(c.Stats, codemode.Stat{Key: "largest unit", Value: fmt.Sprintf("x %s, z %s, y %s m", fnum(L), fnum(D), fnum(H))})
		for k, lim := range [3]float64{L, H, D} {
			if size[k] > lim+tol {
				errorf("size", 0, "", "the unit is %s m along %s; the limit is %s m", fnum(size[k]), "xyz"[k:k+1], fnum(lim))
			}
		}
		if size[0] < 0.01 || size[1] < 0.005 || size[2] < 0.01 {
			errorf("size", 0, "", "the unit is only %s m; check the units (metres)", fv(size))
		}
		realSize(spec, size, errorf, &c)
		cx, cz := (min[0]+max[0])/2, (min[2]+max[2])/2
		if math.Abs(cx) > 0.02+0.05*size[0] || math.Abs(cz) > 0.02+0.05*size[2] {
			warnf(0, "", "the unit is centred at x %s, z %s; centre it on x = 0, z = 0", fnum(cx), fnum(cz))
		}
		if min[1] > 0.01 {
			warnf(0, "", "the unit floats: its bottom is at y = %s; rest it on y = 0", fnum(min[1]))
		}
	} else {
		c.Stats = append(c.Stats,
			codemode.Stat{Key: "envelope", Value: fmt.Sprintf("x [%s, %s], z [%s, %s], y [0, %s]", fnum(-L/2), fnum(L/2), fnum(-D/2), fnum(D/2), fnum(H))},
			codemode.Stat{Key: "belt corridor (keep clear)", Value: fmt.Sprintf("|z| < %s, y %s to %s", fnum(spec.BeltW/2), fnum(spec.BeltY-0.05), fnum(spec.BeltY+spec.Clear))})
		out := 0
		for _, p := range ps {
			var over []string
			if d := math.Max(-L/2-p.Min[0], p.Max[0]-L/2); d > tol {
				over = append(over, fmt.Sprintf("x by %s", fnum(d)))
			}
			if d := math.Max(-D/2-p.Min[2], p.Max[2]-D/2); d > tol {
				over = append(over, fmt.Sprintf("z by %s", fnum(d)))
			}
			if d := p.Max[1] - H; d > tol {
				over = append(over, fmt.Sprintf("y (top %s) by %s", fnum(p.Max[1]), fnum(d)))
			}
			if len(over) > 0 {
				out++
				if out <= 5 {
					errorf("envelope", p.Line, p.Path, "sticks out of the envelope along %s m", strings.Join(over, ", "))
				}
			}
		}
		if out > 5 {
			errorf("envelope", 0, "", "%d parts in all stick out of the envelope", out)
		}
		lo, hi := [3]float64{-L / 2, spec.BeltY - 0.05, -spec.BeltW / 2}, [3]float64{L / 2, spec.BeltY + spec.Clear, spec.BeltW / 2}
		blocked := 0
		for _, p := range ps {
			if d, ok := intersect(p.Min, p.Max, lo, hi, 0.01); ok {
				blocked++
				if blocked <= 5 {
					errorf("belt", p.Line, p.Path, "blocks the belt corridor (inside it by %s m); move it above y = %s, below y = %s or beside |z| >= %s",
						fv(d), fnum(hi[1]), fnum(lo[1]), fnum(spec.BeltW/2))
				}
			}
		}
		if !spec.Append && (size[0] < 0.5*L || size[1] < 0.5*H) {
			warnf(0, "", "the machine uses only %s of its %s m envelope; make it fill more so it reads at its real size", fv(size), fv([3]float64{L, H, D}))
		}
	}

	// Parts buried in another: never seen, still drawn.
	buried := 0
	for i := range all {
		for j := range all {
			if i == j || all[i].Old && all[j].Old {
				continue
			}
			a, b := all[i], all[j] // a inside b?
			if a.volume() > b.volume() || see(b.Part) {
				continue
			}
			d, ok := intersect(a.Min, a.Max, b.Min, b.Max, 0.001)
			if !ok || a.volume() <= 0 {
				continue
			}
			if d[0]*d[1]*d[2] >= 0.8*a.volume() {
				buried++
				if buried <= 6 {
					errorf("overlap", a.Line, a.Path, "is buried inside %q (line %d): it is never seen; move it out or leave it out", b.Path, b.Line)
				}
				break
			}
		}
	}
	if buried > 6 {
		errorf("overlap", 0, "", "%d buried parts in all", buried)
	}
	joined(spec, all, errorf, &c)
	c.Stats = append(c.Stats, groupStats(ps))
	return c
}

// see reports whether a part can be seen through.
func see(p Part) bool { return p.Color == "glass" || (p.Opacity != nil && *p.Opacity < 0.95) }

// intersect is how deep two boxes interpenetrate along each axis, when they do by more than eps
// on every axis (touching faces are fine).
func intersect(amin, amax, bmin, bmax [3]float64, eps float64) ([3]float64, bool) {
	var d [3]float64
	for k := range 3 {
		d[k] = math.Min(amax[k], bmax[k]) - math.Max(amin[k], bmin[k])
		if d[k] <= eps {
			return d, false
		}
	}
	return d, true
}

func groupStats(ps []Placed) codemode.Stat {
	type agg struct {
		n        int
		min, max [3]float64
	}
	var order []string
	by := map[string]*agg{}
	for _, p := range ps {
		top := strings.SplitN(p.Path, "/", 2)[0]
		a := by[top]
		if a == nil {
			a = &agg{min: p.Min, max: p.Max}
			by[top] = a
			order = append(order, top)
		}
		a.n++
		for k := range 3 {
			a.min[k] = math.Min(a.min[k], p.Min[k])
			a.max[k] = math.Max(a.max[k], p.Max[k])
		}
	}
	var out []string
	for i, name := range order {
		if i == 14 {
			out = append(out, fmt.Sprintf("… %d more", len(order)-14))
			break
		}
		a := by[name]
		out = append(out, fmt.Sprintf("%s: %d, %s to %s", name, a.n, fv(a.min), fv(a.max)))
	}
	return codemode.Stat{Key: "top-level parts and groups (count, min to max)", Value: strings.Join(out, "; ")}
}

func fv(v [3]float64) string {
	return fmt.Sprintf("[%s, %s, %s]", fnum(v[0]), fnum(v[1]), fnum(v[2]))
}

func fnum(f float64) string {
	if math.Abs(f) < 5e-4 {
		return "0"
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.3f", f), "0"), ".")
}
