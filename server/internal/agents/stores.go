package agents

import (
	"context"
	"fmt"
	"strings"

	"smartfactory/internal/stores"
)

// storeKinds are the assistants that get the store tools, and which.
var storeKinds = map[string][]string{
	"docs":    {"stock_query", "die_status", "heat_trace", "rnd_projects", "bom_query", "where_used", "material_shortage"},
	"notes":   {"die_status", "heat_trace", "rnd_projects", "stock_query", "bom_query", "where_used", "material_shortage"},
	"service": {"heat_trace", "stock_query", "bom_query", "material_shortage"},
}

// storeToolsOf are the store tools an assistant of a kind works with (none without stores).
func storeToolsOf(have bool, kind string) []string {
	if !have {
		return nil
	}
	return storeKinds[kind]
}

// storePrompt is how an assistant of a kind uses them.
func storePrompt(have bool, kind string) string {
	if !have || storeKinds[kind] == nil {
		return ""
	}
	return `
库房、模具和研发：
- 问库存（某牌号、规格的棒料还有多少，某个标准件够不够，哪些低于安全库存），用 stock_query；出处写【库房】。
- 问模具（某台设备上的模具还能打多少件、在哪、翻新过几次），用 die_status，可以直接填设备编号；出处写【模具台账】。
- 问某个工单用的哪个炉号的料、某炉钢用到了哪些工单、钢厂和质保书，用 heat_trace；出处写【炉号追溯】。客户投诉、质量问题要追到原材料时也先查它。
- 问新品开发、试锻情况，用 rnd_projects；出处写【研发项目】。
- 问某个零件用到哪些物料（标准件、原材料、外购件、辅料）、BOM 是哪个版本、做多少件要多少料，用 bom_query；问某样物料用在哪些零件上，用 where_used；出处写【BOM】。
- 问缺什么料、某样料（如 40Cr 棒料）缺多少、工单齐不齐套，用 material_shortage，它按在做和排队的工单展开 BOM、按排产顺序分库存；出处写【齐套】。
`
}

// storeTools read the stores, the dies and the development projects: what is in stock, how many
// strokes a die has left, which heat an order was forged from, where a new part stands.
func storeTools(svc *stores.Service, company string) []tool {
	match := func(q string, fields ...string) bool {
		q = strings.ToLower(strings.Join(strings.Fields(q), ""))
		if q == "" {
			return true
		}
		text := strings.ToLower(strings.ReplaceAll(strings.Join(fields, " "), " ", ""))
		return strings.Contains(text, q)
	}
	return []tool{
		{name: "stock_query", desc: "查库房：按物料名称、编码、牌号、规格（如 42CrMo、φ80、M24 螺栓）找物料，给出现在的库存、单位、安全库存、是否低于安全库存、库位；棒料还给出每个炉号剩多少、钢厂、质保书号。query 为空时列出全部低于安全库存的物料。棒料库存已经扣掉产线按件数用掉的重量。",
			params: obj(map[string]any{"query": prop("string", "物料名称、编码、牌号或规格（可省）")}),
			run: func(_ context.Context, a args) (any, error) {
				items, err := svc.Items(company, "")
				if err != nil {
					return nil, err
				}
				q := a.str("query")
				var out []map[string]any
				for _, it := range items {
					attrs := ""
					for k, v := range it.Attrs {
						attrs += k + v + " "
					}
					if q == "" && !it.Low || q != "" && !match(q, it.Code, it.Name, attrs) && !match(q, it.Name+attrs) {
						continue
					}
					m := map[string]any{"code": it.Code, "name": it.Name, "attrs": it.Attrs, "stock": it.Stock, "unit": it.Unit, "safety": it.Safety, "low": it.Low, "place": it.Place}
					if len(it.Heats) > 0 {
						var hs []string
						for _, h := range it.Heats {
							hs = append(hs, fmt.Sprintf("炉号 %s（%s，质保书 %s）剩 %v %s", h.No, h.Mill, h.Cert, h.Left, h.Unit))
						}
						m["heats"] = hs
					}
					out = append(out, m)
				}
				if len(items) == 0 {
					return map[string]any{"items": []any{}, "note": "库房里还没有建物料"}, nil
				}
				if out == nil {
					if q == "" {
						return map[string]any{"items": []any{}, "note": "没有低于安全库存的物料"}, nil
					}
					return nil, fmt.Errorf("库房里没有和“%s”对得上的物料", q)
				}
				return map[string]any{"items": out}, nil
			}},
		{name: "die_status", desc: "查模具：按模具号、设备编号（如 HP-101，查装在它上面的模具）、模具种类或零件找模具，给出它在哪（在机、在库、返修中、试制中、已报废）、装在哪台设备上、设计寿命、已打次数、还能打多少次、翻新过几次，和最近发生的事（装模、卸模、寿命提醒、翻新）。已打次数随设备的冲次实时累加。",
			params: obj(map[string]any{"query": prop("string", "模具号、设备编号、模具种类或零件名")}, "query"),
			run: func(_ context.Context, a args) (any, error) {
				ds, err := svc.Dies(company)
				if err != nil {
					return nil, err
				}
				q := a.str("query")
				var out []map[string]any
				for _, d := range ds {
					if !match(q, d.No, d.Asset, d.AssetName, d.Kind, d.Product) {
						continue
					}
					m := map[string]any{"no": d.No, "kind": d.Kind, "product": d.Product, "where": stores.PlaceLabel[d.Place], "machine": d.AssetName,
						"life": d.Life, "strokes": d.Strokes, "left": d.Left, "usedShare": fmt.Sprintf("%.0f%%", float64(d.Strokes)*100/float64(max(d.Life, 1))), "resinks": d.Resinks}
					if h, _ := svc.DieHistory(company, d.No, 5); len(h) > 0 {
						var ev []string
						for _, e := range h {
							ev = append(ev, e.At.In(shanghai).Format("01-02 15:04")+" "+e.Text+"（"+e.Who+"）")
						}
						m["recent"] = ev
					}
					out = append(out, m)
					if len(out) == 10 {
						break
					}
				}
				if out == nil {
					if len(ds) == 0 {
						return nil, fmt.Errorf("模具台账里还没有模具")
					}
					return nil, fmt.Errorf("模具台账里没有和“%s”对得上的模具（设备上没登记模具时也查不到）", q)
				}
				return map[string]any{"dies": out}, nil
			}},
		{name: "heat_trace", desc: "追溯炉号：填炉号，给出这炉钢的牌号、规格、钢厂、质保书、哪天入库多少、剩多少，以及哪条线为哪个工单用了它多少件；填工单号（如 MO-261030-10101），给出这个工单用的是哪个炉号的钢、钢厂、用了多少件。",
			params: obj(map[string]any{"query": prop("string", "炉号或工单号")}, "query"),
			run: func(_ context.Context, a args) (any, error) {
				t, err := svc.TraceOf(company, a.str("query"))
				if err != nil {
					if err == stores.ErrNotFound {
						return nil, fmt.Errorf("库房和生产记录里都没有“%s”：炉号要入库登记过、工单要开工后才查得到", a.str("query"))
					}
					return nil, err
				}
				out := map[string]any{"query": t.Query}
				if t.Heat != nil {
					h := t.Heat
					out["heat"] = map[string]any{"no": h.No, "material": h.ItemName, "grade": h.Grade, "size": h.Size, "mill": h.Mill, "cert": h.Cert,
						"received": fmt.Sprintf("%v %s（%s 入库）", h.Received, h.Unit, h.At.In(shanghai).Format("2006-01-02")), "usedByLines": h.Used, "left": h.Left, "unit": h.Unit}
				}
				var uses []string
				for _, u := range t.Uses {
					uses = append(uses, fmt.Sprintf("%s 工单 %s 用炉号 %s（%s）%d 件，%s 到 %s", u.LineName, u.Order, u.Heat, u.Mill, u.Pieces,
						u.First.In(shanghai).Format("01-02 15:04"), u.Last.In(shanghai).Format("01-02 15:04")))
				}
				out["uses"] = uses
				if t.Note != "" {
					out["note"] = t.Note
				}
				return out, nil
			}},
		{name: "rnd_projects", desc: "查研发（新品开发）项目：按零件号、零件名或客户找项目，给出阶段（立项、模具设计、试锻、检验、量产、暂停）、客户、图纸和模具设计版本、交样日期、负责人、试制模具，以及每次试锻的参数、件数、合格数、结论和发现的问题。query 为空列出全部项目。",
			params: obj(map[string]any{"query": prop("string", "零件号、零件名或客户（可省）")}),
			run: func(_ context.Context, a args) (any, error) {
				ps, err := svc.Projects(company)
				if err != nil {
					return nil, err
				}
				var out []map[string]any
				for _, p := range ps {
					if !match(a.str("query"), p.Part, p.Name, p.Customer) {
						continue
					}
					m := map[string]any{"part": p.Part, "name": p.Name, "customer": p.Customer, "stage": p.Stage, "drawing": p.Drawing, "dieDesign": p.DieDesign,
						"due": p.Due, "owner": p.Owner, "grade": p.Grade, "note": p.Note}
					var dies, trials []string
					for _, d := range p.Dies {
						dies = append(dies, d.No+" "+stores.PlaceLabel[d.Place])
					}
					for _, t := range p.Trials {
						trials = append(trials, fmt.Sprintf("%s 用 %s，%s；%d 件合格 %d，%s：%s", t.At.In(shanghai).Format("01-02"), t.Die, t.Params, t.Qty, t.Good, t.Result, t.Findings))
					}
					m["dies"], m["trials"] = dies, trials
					out = append(out, m)
				}
				if out == nil {
					return map[string]any{"projects": []any{}, "note": "没有对得上的研发项目"}, nil
				}
				return map[string]any{"projects": out}, nil
			}},
	}
}
