package ops

import (
	"context"
	"encoding/json"
	"strings"
	"unicode/utf8"

	"smartfactory/internal/agents"
	"smartfactory/internal/auth"
	"smartfactory/internal/shapes"
)

// ---- shapes drawn by the AI ----

// ShapeRequest names what to draw: a machine kind or a stage of a product of an industry pack.
type ShapeRequest struct {
	Industry string `json:"industry"`
	Target   string `json:"target"` // machine | product
	Key      string `json:"key"`
	Stage    string `json:"stage,omitempty"`
	Brief    string `json:"brief"`
}

func (o *Ops) shapeSpec(in ShapeRequest) (shapes.Spec, string, error) {
	js := o.Engine.Industry(in.Industry)
	if js == "null" {
		return shapes.Spec{}, "", fail(404, "没有行业包 %s", in.Industry)
	}
	raw := json.RawMessage(js)
	switch in.Target {
	case "machine":
		spec, label, err := shapes.MachineSpec(raw, in.Key)
		if err != nil {
			return spec, "", fail(404, "%s", err)
		}
		return spec, label, nil
	case "product":
		spec, name, now, err := shapes.ProductSpec(raw, in.Key, in.Stage)
		if err != nil {
			return spec, "", fail(404, "%s", err)
		}
		what := name + "（工序状态 " + in.Stage + "）"
		if now[0] > 0 {
			what += "，现在的样子长 " + metres(now[0]) + " 米、宽 " + metres(now[1]) + " 米、高 " + metres(now[2]) + " 米，没说要改大小就保持差不多"
		}
		return spec, what, nil
	}
	return shapes.Spec{}, "", fail(400, "target 是 machine 或 product")
}

func metres(f float64) string {
	b, _ := json.Marshal(float64(int(f*100+0.5)) / 100)
	return string(b)
}

// DrawShape has the AI draw a machine's or a product's shape from a description, checked to fit
// the twin. Nothing is saved: emit sees the attempts, then the drawing ("shape").
func (o *Ops) DrawShape(ctx context.Context, who auth.Who, in ShapeRequest, emit func(agents.Event)) error {
	if err := Admin(who); err != nil {
		return err
	}
	if o.Agents == nil || !o.Agents.Ready() {
		return fail(503, "%s", agents.ErrNoModel)
	}
	in.Brief = strings.TrimSpace(in.Brief)
	if in.Brief == "" || utf8.RuneCountInString(in.Brief) > 1000 {
		return fail(400, "描述要 1～1000 字")
	}
	spec, what, err := o.shapeSpec(in)
	if err != nil {
		return err
	}
	d, err := o.Agents.DrawShape(ctx, who, spec, what, in.Brief, emit)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	emit(agents.Event{Kind: "shape", Result: map[string]any{"drawing": d, "spec": map[string]any{
		"target": in.Target, "envelope": spec.Envelope, "beltY": spec.BeltY, "beltW": spec.BeltW, "clear": spec.Clear,
	}}})
	text := d.Answer
	if !d.OK {
		text = strings.TrimSpace(text + "\n还有没通过的检查，可以换个说法再画一次。")
	}
	emit(agents.Event{Kind: "complete", Text: text})
	return nil
}

// SaveShape puts drawn parts into an industry pack: a machine kind's look (an empty frame and
// these parts, at size) or a product stage's parts. The pack is checked and saved as a whole.
func (o *Ops) SaveShape(who auth.Who, via string, id, target, key, stage string, parts []shapes.Part, size *[3]float64) (json.RawMessage, error) {
	if err := Admin(who); err != nil {
		return nil, err
	}
	if !who.Everywhere() {
		return nil, fail(403, "行业包是所有公司共用的，要所有公司的管理员令牌")
	}
	js := o.Engine.Industry(id)
	if js == "null" {
		return nil, fail(404, "没有行业包 %s", id)
	}
	limit := shapes.MaxMachineParts
	if target == "product" {
		limit = shapes.MaxProductParts
	}
	if len(parts) == 0 || len(parts) > limit {
		return nil, fail(400, "零件要 1～%d 个", limit)
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(js), &cfg); err != nil {
		return nil, err
	}
	var list []any
	b, _ := json.Marshal(parts)
	json.Unmarshal(b, &list)
	name := key
	switch target {
	case "machine":
		m, _ := dig(cfg, "machines", key).(map[string]any)
		if m == nil {
			return nil, fail(404, "行业包里没有设备 %s", key)
		}
		look, _ := m["look"].(map[string]any)
		if look == nil {
			look = map[string]any{}
		}
		// the parts are the whole machine: an empty base (frame) under them, keeping the colours
		look["archetype"] = "frame"
		delete(look, "tool")
		delete(look, "fence")
		if size != nil {
			look["size"] = []float64{size[0], size[1], size[2]}
		}
		look["parts"] = list
		m["look"] = look
		if l, ok := m["label"].(string); ok {
			name = l
		}
	case "product":
		st, _ := dig(cfg, "products", key, "stages", stage).(map[string]any)
		if st == nil {
			return nil, fail(404, "产品 %s 没有工序状态 %s", key, stage)
		}
		st["parts"] = list
		if n, ok := dig(cfg, "products", key, "name").(string); ok {
			name = n + " " + stage
		}
	default:
		return nil, fail(400, "target 是 machine 或 product")
	}
	raw, _ := json.Marshal(cfg)
	res := o.Engine.CreateIndustry(raw)
	o.Audit.Record(who, via, "*", "industry.shape", id+"/"+key, name, errOf(res))
	if !res.OK {
		return nil, rejected(res)
	}
	return res.Detail, nil
}

func dig(v any, path ...string) any {
	for _, k := range path {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[k]
	}
	return v
}
