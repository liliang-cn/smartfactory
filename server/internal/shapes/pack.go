package shapes

import (
	"encoding/json"
	"fmt"
	"math"
)

// The frame a shape is drawn in comes from its industry pack, worked out the way the twin lays
// out a line (core/layout.ts): the belt is as wide and as high as the pack's biggest product
// needs, and a machine's envelope is its look size grown to fit the belt.

type pack struct {
	Flow     string `json:"flow"`
	Products map[string]struct {
		Name   string `json:"name"`
		Stages map[string]struct {
			Parts []Part `json:"parts"`
		} `json:"stages"`
	} `json:"products"`
	Machines map[string]struct {
		Label string `json:"label"`
		Look  struct {
			Archetype string      `json:"archetype"`
			Size      *[3]float64 `json:"size"`
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
func MachineSpec(raw json.RawMessage, key string) (Spec, string, error) {
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
	return Spec{Target: Machine, Envelope: env, BeltY: beltY, BeltW: beltW, Clear: clear}, m.Label, nil
}

// ProductSpec is the frame for a stage of product key, and the stage's current size as a hint
// [l, d, h].
func ProductSpec(raw json.RawMessage, key, stage string) (Spec, string, [3]float64, error) {
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
	return Spec{Target: Product, Envelope: ProductMax}, pr.Name, [3]float64{l, d, h}, nil
}
