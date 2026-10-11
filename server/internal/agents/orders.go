package agents

import (
	"context"
	"strings"
	"time"

	"smartfactory/internal/erp"
)

// ordersTool finds the production orders the company's ERP sent: what a customer ordered, how
// far it is, which line runs it, when it is due.
func ordersTool(st *erp.Store, company string) tool {
	return tool{name: "production_orders",
		desc: "查生产工单（ERP 下达的）：按订单号、客户、料号或产线找，返回状态（排队/生产中/完成/撤回）、做到多少件、报废、产线、交期、开工和完工时间。",
		params: obj(map[string]any{
			"query":  prop("string", "订单号、客户名、料号或产线名（可省：列出在做和排队的）"),
			"status": enum("只看这个状态（可省）", []string{erp.Queued, erp.Running, erp.Done, erp.Cancelled}),
		}),
		run: func(_ context.Context, a args) (any, error) {
			f := erp.Filter{Q: a.str("query"), Limit: 20}
			if s := a.str("status"); s != "" {
				f.Status = []string{s}
			} else if f.Q == "" {
				f.Status = []string{erp.Running, erp.Queued}
			}
			list, err := st.List(company, f)
			if err != nil {
				return nil, err
			}
			label := map[string]string{erp.Queued: "排队", erp.Running: "生产中", erp.Done: "完成", erp.Cancelled: "撤回"}
			day := func(t *time.Time) string {
				if t == nil {
					return ""
				}
				return t.In(shanghai).Format("2006-01-02 15:04")
			}
			out := make([]map[string]any, len(list))
			for i, o := range list {
				m := map[string]any{
					"no": o.No, "status": label[o.Status], "part": strings.TrimSpace(o.PartNo + " " + o.Name), "qty": o.Qty, "good": o.Good, "scrap": o.Scrap,
					"line": strings.TrimSpace(o.PlantName + " " + o.LineName), "customer": o.Customer, "due": day(o.Due),
					"started": day(o.Started), "finished": day(o.Finished),
				}
				if o.Due != nil && o.Finished == nil && time.Now().After(*o.Due) && (o.Status == erp.Queued || o.Status == erp.Running) {
					m["late"] = true
				}
				if o.Problem != "" {
					m["problem"] = o.Problem
				}
				out[i] = m
			}
			if len(out) == 0 {
				return map[string]any{"orders": out, "note": "没有对得上的生产工单（公司可能没接 ERP）"}, nil
			}
			return map[string]any{"orders": out}, nil
		}}
}
