package ops

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"

	"smartfactory/internal/agents"
	"smartfactory/internal/auth"
	"smartfactory/internal/engine"
	"smartfactory/internal/models"
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
	// Append adds the drawn parts to the look as it is, instead of replacing the whole look
	// (machine) or stage (product).
	Append bool `json:"append,omitempty"`
}

func (o *Ops) shapeSpec(in ShapeRequest) (shapes.Spec, string, error) {
	js := o.Engine.Industry(in.Industry)
	if js == "null" {
		return shapes.Spec{}, "", fail(404, "没有行业包 %s", in.Industry)
	}
	raw := json.RawMessage(js)
	switch in.Target {
	case "machine":
		spec, label, err := shapes.MachineSpec(raw, in.Key, in.Append)
		if err != nil {
			return spec, "", fail(404, "%s", err)
		}
		return spec, label, nil
	case "product":
		spec, name, now, err := shapes.ProductSpec(raw, in.Key, in.Stage, in.Append)
		if err != nil {
			return spec, "", fail(404, "%s", err)
		}
		what := name + "（工序状态 " + in.Stage + "）"
		if spec.RealFrom == "" && now[0] > 0 {
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
		"target": in.Target, "envelope": spec.Envelope, "beltY": spec.BeltY, "beltW": spec.BeltW, "clear": spec.Clear, "append": spec.Append,
	}}})
	text := d.Answer
	if !d.OK {
		text = strings.TrimSpace(text + "\n还有没通过的检查，可以换个说法再画一次。")
	}
	emit(agents.Event{Kind: "complete", Text: text})
	return nil
}

// SaveShape puts drawn parts into an industry pack: a machine kind's look (an empty frame and
// these parts, at size) or a product stage's parts. With add, the parts are put after the ones
// already there and the machine keeps its body, instead of replacing the whole look or stage.
//
// A pack of your own is checked and saved as a whole. A built-in pack is left as it ships: only
// the machine's look or the stage's parts are kept, as an overlay laid on top of it (ResetShape
// takes them off again).
func (o *Ops) SaveShape(who auth.Who, via string, id, target, key, stage string, parts []shapes.Part, size *[3]float64, add bool) (json.RawMessage, error) {
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
	// what of the pack is changed, as the overlay of a built-in pack keeps it
	var kept map[string]any
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
		if add {
			// the look stays as it is; the new parts come after the old ones
			old, _ := look["parts"].([]any)
			if len(old)+len(list) > limit {
				return nil, fail(400, "原有 %d 个零件，再加 %d 个超过了 %d 个的上限", len(old), len(list), limit)
			}
			list = append(append([]any{}, old...), list...)
		} else {
			// the parts are the whole machine: an empty base (frame) under them, keeping the colours
			look["archetype"] = "frame"
			delete(look, "tool")
			delete(look, "fence")
		}
		if size != nil {
			look["size"] = []float64{size[0], size[1], size[2]}
		}
		look["parts"] = list
		m["look"] = look
		kept = map[string]any{"machines": map[string]any{key: map[string]any{"look": look}}}
		if l, ok := m["label"].(string); ok {
			name = l
		}
	case "product":
		st, _ := dig(cfg, "products", key, "stages", stage).(map[string]any)
		if st == nil {
			return nil, fail(404, "产品 %s 没有工序状态 %s", key, stage)
		}
		if add {
			old, _ := st["parts"].([]any)
			if len(old)+len(list) > limit {
				return nil, fail(400, "原有 %d 个零件，再加 %d 个超过了 %d 个的上限", len(old), len(list), limit)
			}
			list = append(append([]any{}, old...), list...)
		}
		st["parts"] = list
		kept = map[string]any{"products": map[string]any{key: map[string]any{"stages": map[string]any{stage: map[string]any{"parts": list}}}}}
		if n, ok := dig(cfg, "products", key, "name").(string); ok {
			name = n + " " + stage
		}
	default:
		return nil, fail(400, "target 是 machine 或 product")
	}
	if o.Engine.IsBuiltinIndustry(id) {
		res := o.setOverlay(id, kept)
		o.Audit.Record(who, via, "*", "industry.shape", id+"/"+key, name, errOf(res))
		if !res.OK {
			return nil, rejected(res)
		}
		return res.Detail, nil
	}
	raw, _ := json.Marshal(cfg)
	res := o.Engine.CreateIndustry(raw)
	o.Audit.Record(who, via, "*", "industry.shape", id+"/"+key, name, errOf(res))
	if !res.OK {
		return nil, rejected(res)
	}
	return res.Detail, nil
}

// setOverlay lays more drawn shapes ({machines: {key: {look}}, products: {key: {stages: {stage:
// {parts}}}}}) on the ones a built-in pack already has on this installation.
func (o *Ops) setOverlay(id string, more map[string]any) engine.ConfigResult {
	cur := map[string]any{}
	if b := o.Engine.ShapeOverlay(id); b != nil {
		json.Unmarshal(b, &cur)
	}
	for _, kind := range []string{"machines", "products"} {
		add, _ := more[kind].(map[string]any)
		have, _ := cur[kind].(map[string]any)
		if have == nil {
			have = map[string]any{}
		}
		for k, v := range add {
			if kind == "products" { // stage by stage
				stages, _ := dig(have, k, "stages").(map[string]any)
				if stages == nil {
					stages = map[string]any{}
				}
				for s, p := range dig(v, "stages").(map[string]any) {
					stages[s] = p
				}
				have[k] = map[string]any{"stages": stages}
				continue
			}
			have[k] = v
		}
		cur[kind] = have
	}
	cur["id"] = id
	raw, _ := json.Marshal(cur)
	return o.Engine.SetShapeOverlay(raw)
}

// ShapeOverlay is what was drawn into a built-in pack on this installation (nil when nothing).
func (o *Ops) ShapeOverlay(id string) json.RawMessage { return o.Engine.ShapeOverlay(id) }

// ResetShape takes shapes drawn into a built-in pack off again, bringing back the look it ships
// with: one machine kind (target machine, key), one product stage (product, key, stage), or, with
// no target, all of them.
func (o *Ops) ResetShape(who auth.Who, via string, id, target, key, stage string) (json.RawMessage, error) {
	if err := Admin(who); err != nil {
		return nil, err
	}
	if !who.Everywhere() {
		return nil, fail(403, "行业包是所有公司共用的，要所有公司的管理员令牌")
	}
	if !o.Engine.IsBuiltinIndustry(id) {
		return nil, fail(400, "%s 不是内置行业包，没有「内置外形」可以恢复", id)
	}
	cur := map[string]any{}
	if b := o.Engine.ShapeOverlay(id); b != nil {
		json.Unmarshal(b, &cur)
	}
	name := id
	switch target {
	case "":
		cur = map[string]any{}
	case "machine":
		if m, ok := cur["machines"].(map[string]any); ok {
			delete(m, key)
		}
		name = key
	case "product":
		// one stage, or (no stage) all the stages of the product
		if st, ok := dig(cur, "products", key, "stages").(map[string]any); ok {
			delete(st, stage)
			if len(st) == 0 || stage == "" {
				delete(cur["products"].(map[string]any), key)
			}
		}
		name = strings.TrimSpace(key + " " + stage)
	default:
		return nil, fail(400, "target 是 machine 或 product")
	}
	cur["id"] = id
	raw, _ := json.Marshal(cur)
	res := o.Engine.SetShapeOverlay(raw)
	o.Audit.Record(who, via, "*", "industry.shape-reset", id+"/"+key, name, errOf(res))
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

// SearchModels looks in the catalogue of ready-made 3D models for things a look can place
// (shape part "model"), for the console and the pack authors.
func (o *Ops) SearchModels(ctx context.Context, who auth.Who, q string, n int) (*models.Found, error) {
	if err := Admin(who); err != nil {
		return nil, err
	}
	if o.Models == nil {
		return nil, fail(503, "这台服务器没有数据目录，用不了模型库")
	}
	q = strings.TrimSpace(q)
	if q == "" || utf8.RuneCountInString(q) > 100 {
		return nil, fail(400, "q 写要找什么（英文），100 字以内")
	}
	f, err := o.Models.Search(ctx, q, cmp.Or(n, 12))
	if errors.Is(err, models.ErrBusy) {
		return nil, fail(429, "%s", err)
	}
	if err != nil {
		return nil, fail(502, "%s", err)
	}
	return f, nil
}
