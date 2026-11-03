package agents

import (
	"context"
	"fmt"
	"math"
	"strings"

	"smartfactory/internal/erp"
	"smartfactory/internal/stores"
)

// The BOM tools: what goes into a part (by its BOM in effect), where a component is used, and
// what the open orders are short of.

func amount(v float64, unit string) string {
	s := fmt.Sprintf("%.3f", v)
	s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	return s + " " + unit
}

// openDemand is what the company's running and queued orders still have to make, in turn.
func openDemand(orders *erp.Store, company string) ([]stores.Demand, error) {
	if orders == nil {
		return nil, nil
	}
	list, err := orders.List(company, erp.Filter{Status: []string{"running", "queued"}, Limit: 1000})
	if err != nil {
		return nil, err
	}
	out := make([]stores.Demand, 0, len(list))
	for _, o := range list {
		out = append(out, stores.Demand{No: o.No, Part: o.PartNo, Product: o.Product, Qty: math.Max(0, float64(o.Qty-o.Good))})
	}
	return out, nil
}

func needLine(n stores.Need) stores.BOMLine {
	return stores.BOMLine{Code: n.Code, Name: n.Name, Kind: n.Kind, Spec: n.Spec, Grade: n.Grade}
}

func bomTools(svc *stores.Service, orders *erp.Store, company string) []tool {
	return []tool{
		{name: "bom_query", desc: "查 BOM（物料清单）：按零件号或零件名（如 QZ-B20-4、曲轴、连杆）找生效版本，给出版本、图纸版本、生效日期，和逐层的物料：类型（自制件、外购件、标准件、原材料、辅料）、名称规格牌号、每件用量（已含损耗）、单位、在哪道工序用。问「某产品用到哪些标准件/原材料」时看 kind 对得上的行。给 qty 时按这个数量展开，算出每样物料一共要多少、库房有多少、缺多少。",
			params: obj(map[string]any{"query": prop("string", "零件号或零件名"), "qty": prop("number", "要做多少件（可省；给了就算齐套）")}, "query"),
			run: func(_ context.Context, a args) (any, error) {
				parts, err := svc.Parts(company)
				if err != nil {
					return nil, err
				}
				q := strings.ToLower(strings.Join(strings.Fields(a.str("query")), ""))
				var hits []stores.PartRow
				for _, p := range parts {
					text := strings.ToLower(strings.ReplaceAll(p.Part+p.Name+p.Family, " ", ""))
					if p.Active != "" && (strings.Contains(text, q) || strings.EqualFold(p.Part, a.str("query"))) {
						hits = append(hits, p)
					}
				}
				if len(hits) == 0 {
					for _, p := range parts {
						if strings.Contains(strings.ToLower(p.Part+p.Name), q) {
							return nil, fmt.Errorf("%s %s 还没有生效的 BOM", p.Part, p.Name)
						}
					}
					return nil, fmt.Errorf("没有和“%s”对得上的零件 BOM", a.str("query"))
				}
				qty := 0.0
				if v, ok := a["qty"].(float64); ok && v > 0 {
					qty = v
				}
				var out []map[string]any
				for _, p := range hits[:min(len(hits), 4)] {
					x, err := svc.Explode(company, p.Part, "", max(qty, 1))
					if err != nil {
						continue
					}
					var lines []string
					byKind := map[string][]string{}
					for _, r := range x.Rows {
						l := fmt.Sprintf("%s%s %s（%s）每件 %s", strings.Repeat("  ", r.Level-1), stores.KindLabel(r.Kind), r.Label(), r.Code, amount(r.Per, r.Unit))
						if r.Scrap > 0 {
							l += fmt.Sprintf("（含损耗 %v%%）", r.Scrap)
						}
						if r.Op != "" {
							l += "，" + r.Op
						}
						if r.Sub != "" {
							l += fmt.Sprintf("，下面是它自己的 BOM（%s 版）", r.Rev)
						}
						lines = append(lines, l)
						k := stores.KindLabel(r.Kind)
						byKind[k] = append(byKind[k], r.Label())
					}
					m := map[string]any{"part": x.Part, "name": x.Name, "rev": x.Rev, "drawing": x.Drawing, "effective": x.Effective, "lines": lines, "byKind": byKind}
					if qty > 0 {
						var needs []string
						for _, n := range x.Needs {
							s := fmt.Sprintf("%s %s：要 %s，库房 %s", stores.KindLabel(n.Kind), strings.TrimSpace(n.Grade+" "+n.Name+" "+n.Spec), amount(n.Need, n.Unit), amount(n.Stock, n.Unit))
							switch {
							case n.Problem != "":
								s += "，" + n.Problem
							case n.Short > 1e-9:
								s += "，缺 " + amount(n.Short, n.Unit)
							default:
								s += "，够"
							}
							needs = append(needs, s)
						}
						m["for"], m["needs"], m["shortItems"] = qty, needs, x.Short
					}
					out = append(out, m)
				}
				return map[string]any{"boms": out}, nil
			}},
		{name: "where_used", desc: "反查物料用在哪：按物料编码、名称、牌号或规格（如 40Cr、油道螺塞、M10、GB-42CrMo-80），列出生效 BOM 里用到它的零件、每件用多少、经过哪个半成品、在哪道工序用。可以带类型词，如「40Cr 棒料」。问某个产品用到什么，用 bom_query。",
			params: obj(map[string]any{"query": prop("string", "物料编码、名称、牌号或规格")}, "query"),
			run: func(_ context.Context, a args) (any, error) {
				us, err := svc.WhereUsed(company, a.str("query"))
				if err != nil {
					return nil, err
				}
				if len(us) == 0 {
					return nil, fmt.Errorf("生效的 BOM 里没有用到“%s”的", a.str("query"))
				}
				var out []string
				for _, u := range us[:min(len(us), 40)] {
					s := fmt.Sprintf("%s %s（%s 版）每件用 %s %s", u.Part, u.Name, u.Rev, u.Item, amount(u.Per, u.Unit))
					if u.Via != "" {
						s += "，在" + u.Via + "里"
					}
					if u.Op != "" {
						s += "，" + u.Op
					}
					out = append(out, s)
				}
				return map[string]any{"uses": out, "count": len(us)}, nil
			}},
		{name: "material_shortage", desc: "算齐套和缺料：把在做和排队的工单按 BOM 展开，按排产顺序先到先得分配库存，给出每张工单够不够、缺什么，以及全部工单一共要多少、库房有多少、缺多少。query 填物料（如 40Cr 棒料、螺塞、42CrMo）只看这一样；为空列出所有缺的。没有 BOM 的工单单独列出。",
			params: obj(map[string]any{"query": prop("string", "物料名称、编码、牌号或规格（可省）")}),
			run: func(_ context.Context, a args) (any, error) {
				ds, err := openDemand(orders, company)
				if err != nil {
					return nil, err
				}
				if len(ds) == 0 {
					return map[string]any{"note": "现在没有在做或排队的工单（ERP 下达或后台录入的），算不了缺料"}, nil
				}
				k, err := svc.Kit(company, ds)
				if err != nil {
					return nil, err
				}
				q := a.str("query")
				var items []string
				for _, t := range k.Totals {
					if q != "" && !stores.Matches(q, needLine(t.Need)) {
						continue
					}
					if q == "" && t.Short <= 1e-9 {
						continue
					}
					s := fmt.Sprintf("%s %s：工单一共要 %s，库房 %s", stores.KindLabel(t.Kind), strings.TrimSpace(t.Grade+" "+t.Name+" "+t.Spec), amount(t.Need.Need, t.Unit), amount(t.Stock, t.Unit))
					switch {
					case t.Problem != "":
						s += "，" + t.Problem
					case t.Short > 1e-9:
						s += "，缺 " + amount(t.Short, t.Unit)
					default:
						s += "，够"
					}
					items = append(items, s+"（工单 "+strings.Join(t.Orders, "、")+"）")
				}
				var orderLines, noBOM []string
				for _, o := range k.Orders {
					if o.Rev == "" {
						noBOM = append(noBOM, o.No+" "+o.Part)
						continue
					}
					s := fmt.Sprintf("%s %s %v 件：", o.No, o.Part, o.Qty)
					if o.Ready {
						s += "齐套"
					} else {
						var short []string
						for _, n := range o.Needs {
							if n.Short > 1e-9 {
								short = append(short, n.Name+" 缺 "+amount(n.Short, n.Unit))
							}
						}
						s += strings.Join(short, "，")
					}
					orderLines = append(orderLines, s)
				}
				out := map[string]any{"items": items, "orders": orderLines}
				if len(noBOM) > 0 {
					out["noBOM"] = noBOM
				}
				if len(items) == 0 {
					if q != "" {
						out["note"] = fmt.Sprintf("在做和排队的工单都用不到“%s”", q)
					} else {
						out["note"] = "在做和排队的工单物料都齐套"
					}
				}
				return out, nil
			}},
	}
}
