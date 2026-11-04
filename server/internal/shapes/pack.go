package shapes

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
)

// The frame a shape is drawn in comes from its industry pack, worked out the way the twin lays
// out a line (core/layout.ts): the belt is as wide and as high as the pack's biggest product
// needs, and a machine's envelope is its look size grown to fit the belt.

type pack struct {
	Flow     string `json:"flow"`
	Products map[string]struct {
		Name     string      `json:"name"`
		Weight   float64     `json:"weight"`
		Material string      `json:"material"`
		Size     *[3]float64 `json:"size"`
		Stages   map[string]struct {
			Parts []Part `json:"parts"`
		} `json:"stages"`
	} `json:"products"`
	Machines map[string]struct {
		Label string `json:"label"`
		Look  struct {
			Archetype string      `json:"archetype"`
			Size      *[3]float64 `json:"size"`
			Parts     []Part      `json:"parts"`
		} `json:"look"`
	} `json:"machines"`
}

// Dims is the bounding size of parts as the twin measures a product stage: l along x, h, d along
// z, each from the origin outwards (so an off-centre part counts twice its offset).
func Dims(parts []Part) (l, h, d float64) {
	for _, p := range parts {
		min, max := PartBox(p)
		l = math.Max(l, 2*math.Max(math.Abs(min[0]), math.Abs(max[0])))
		h = math.Max(h, max[1])
		d = math.Max(d, 2*math.Max(math.Abs(min[2]), math.Abs(max[2])))
	}
	return
}

func readPack(raw json.RawMessage) (*pack, error) {
	var p pack
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("行业包读不出来：%w", err)
	}
	return &p, nil
}

// MachineSpec is the frame for machine key of the pack: its envelope and the belt through it.
// With add, the drawing is added to the machine's look as it is (its body and parts stay), and the
// spec carries the parts already there.
func MachineSpec(raw json.RawMessage, key string, add bool) (Spec, string, error) {
	p, err := readPack(raw)
	if err != nil {
		return Spec{}, "", err
	}
	m, ok := p.Machines[key]
	if !ok {
		return Spec{}, "", fmt.Errorf("行业包里没有设备 %s", key)
	}
	var mh, md float64
	for _, pr := range p.Products {
		for _, st := range pr.Stages {
			_, h, d := Dims(st.Parts)
			mh, md = math.Max(mh, h), math.Max(md, d)
		}
	}
	walk := p.Flow == "walk"
	beltW := math.Max(0.9, md+0.3)
	beltY := 0.9
	switch {
	case walk:
		beltW, beltY = math.Max(0.9, md+0.8), 0.05
	case mh > 1.2:
		beltY = 0.5
	}
	beltW = math.Round(beltW*10) / 10
	base, ok := DefaultSize["frame"], false
	if m.Look.Size != nil {
		base, ok = *m.Look.Size, true
	}
	if !ok {
		if s, has := DefaultSize[m.Look.Archetype]; has {
			base = s
		}
	}
	env := [3]float64{base[0], math.Max(base[1], beltW+1.0), math.Max(base[2], beltY+mh+0.9)}
	for i := range env {
		env[i] = math.Round(env[i]*100) / 100
	}
	clear := math.Round(math.Max(0.4, mh+0.15)*100) / 100
	spec := Spec{Target: Machine, Envelope: env, BeltY: beltY, BeltW: beltW, Clear: clear}
	if add {
		spec.Append, spec.Body, spec.Existing = true, m.Look.Archetype, m.Look.Parts
	}
	return spec, m.Label, nil
}

// ProductSpec is the frame for a stage of product key, and the stage's current size as a hint
// [l, d, h]. With add, the drawing is added to the stage's parts. The spec carries the product's
// real size when the pack gives it (size) or its weight lets one be worked out.
func ProductSpec(raw json.RawMessage, key, stage string, add bool) (Spec, string, [3]float64, error) {
	p, err := readPack(raw)
	if err != nil {
		return Spec{}, "", [3]float64{}, err
	}
	pr, ok := p.Products[key]
	if !ok {
		return Spec{}, "", [3]float64{}, fmt.Errorf("行业包里没有产品 %s", key)
	}
	st, ok := pr.Stages[stage]
	if !ok {
		return Spec{}, "", [3]float64{}, fmt.Errorf("产品 %s 没有工序状态 %s", pr.Name, stage)
	}
	l, h, d := Dims(st.Parts)
	spec := Spec{Target: Product, Envelope: ProductMax, Name: pr.Name}
	spec.Real, spec.RealFrom = RealSize(pr.Size, pr.Weight, pr.Material)
	if add {
		spec.Append, spec.Existing = true, st.Parts
	}
	return spec, pr.Name, [3]float64{l, d, h}, nil
}

// RealSize is how big one unit of a product really is, [l, d, h] in metres: what the pack says
// (size), or else worked out from its weight as a compact bounding box of the material (steel when
// the material reads like an alloy), 4 : 1.5 : 1 long. The second result says where it came from
// (RealGiven, RealEstimated); it is zero, with an empty source, when there is nothing to go on.
func RealSize(size *[3]float64, weight float64, material string) ([3]float64, string) {
	if size != nil && size[0] > 0 && size[1] > 0 && size[2] > 0 {
		return *size, RealGiven
	}
	if !(weight > 0) {
		return [3]float64{}, ""
	}
	rho := 1200.0
	if alloy.MatchString(material) {
		rho = 7850
	}
	s := math.Cbrt(weight / rho / 0.3 / 6) // a box of 4s × 1.5s × s, 30 % filled
	return [3]float64{round(4*s, 1e3), round(1.5*s, 1e3), round(s, 1e3)}, RealEstimated
}

var alloy = regexp.MustCompile(`Cr|Mn|Mo|Ni|钢|^Q\d|^C\d`)
