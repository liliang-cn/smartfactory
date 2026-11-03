package agents

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"smartfactory/internal/erp"
	"smartfactory/internal/plan"
)

// The production plan: how the plan in force stands (who is late and why), and what an urgent
// order would do to it — answered by the scheduler, not guessed.

var planPrompt = `
生产计划：
- 问生产计划、哪张工单会晚、为什么晚、哪条线排满了、什么时候能做完，用 plan_status；出处写【生产计划】。
- 问「插一个急单会怎样」「下周三要 500 件 X，赶得上吗、会影响谁」，用 plan_whatif，填料号或产品、数量和交期（把「下周三」这种说法换成具体日期）；照它说的回答：急单排在哪条线、什么时候做完、赶不赶得上，被往后推的工单和客户，有没有因此误期的。它只是推算，不会改计划；要真插单，让计划员在「生产计划」页重新排、发布。
`

// The plan tools' descriptions, shared with the console assistant.
const (
	PlanStatusDesc = "看生产计划（排产）：发布的计划（没发布过就是现在的排队顺序）的按期率、误期多少、换型次数、各线负荷，每张工单在哪条线、几点开工几点做完、晚多少和原因（产能、缺料、模具、交期太紧），缺料先不排的工单，以及排完以后新来、撤回的工单。"
	PlanWhatIfDesc = "推算插一张急单对生产计划的影响（不改计划）：在能做这个料号的产线里找最不耽误事的位置插进去，返回急单排在哪、什么时候做完、赶不赶得上交期，被往后推的工单（推后多少、会不会因此误期、哪个客户），以及前后的按期率和误期小时数。"
)

func planTools(svc *plan.Service, company string) []tool {
	return []tool{
		{name: "plan_status", desc: PlanStatusDesc,
			params: obj(map[string]any{}),
			run: func(_ context.Context, _ args) (any, error) {
				return svc.Status(company)
			}},
		{name: "plan_whatif", desc: PlanWhatIfDesc,
			params: obj(map[string]any{
				"part":     prop("string", "料号或产品名，如 QZ-B20-4 或 发动机曲轴"),
				"qty":      prop("integer", "数量（件）"),
				"due":      prop("string", "交期，如 2026-11-11 或 2026-11-11 17:00（只写日期按当天 17:00）"),
				"customer": prop("string", "客户（可省）"),
			}, "part", "qty", "due"),
			run: func(_ context.Context, a args) (any, error) {
				in, err := RushIn(a.str("part"), a.num("qty", 0), a.str("due"), a.str("customer"))
				if err != nil {
					return nil, err
				}
				v, err := svc.Rush(company, in)
				var p *plan.Problems
				if errors.As(err, &p) {
					return map[string]any{"error": p.Error()}, nil
				}
				if err != nil {
					return nil, err
				}
				return RushAnswer(v), nil
			}},
	}
}

// RushIn reads an urgent order as an assistant gives it; a due date without a time is 17:00.
func RushIn(part string, qty int, due, customer string) (plan.RushInput, error) {
	t, err := erp.ParseTime(due)
	if err != nil {
		return plan.RushInput{}, err
	}
	if len(strings.TrimSpace(due)) == 10 {
		t = t.Add(17 * time.Hour)
	}
	return plan.RushInput{Part: part, Qty: qty, Due: t, Customer: customer}, nil
}

// RushAnswer is what an urgent order would do, as the assistants tell it: where it goes, when it
// is done, whom it pushes back.
func RushAnswer(v *plan.Impact) map[string]any {
	day := func(t time.Time) string { return t.In(shanghai).Format("01-02 15:04") }
	pushed := []map[string]any{}
	for _, x := range v.Pushed {
		m := map[string]any{"no": x.No, "part": x.Part, "line": x.Line, "was": day(x.Was), "now": day(x.Now), "delayHours": fmt.Sprintf("%.1f", x.DelayMin/60)}
		if x.Customer != "" {
			m["customer"] = x.Customer
		}
		if !x.Due.IsZero() {
			m["due"] = day(x.Due)
		}
		if x.Late && !x.WasLate {
			m["becomesLate"] = true
		} else if x.Late {
			m["alreadyLate"] = true
		}
		pushed = append(pushed, m)
	}
	o := v.Order
	return map[string]any{
		"summary": v.Note, "comparedWith": v.Against,
		"rush": map[string]any{"line": o.LineName, "place": o.Seq, "start": day(o.Start), "end": day(o.End), "due": day(o.Due), "onTime": v.OnTime,
			"lateHours": fmt.Sprintf("%.1f", o.LateMin/60), "changeoverMinutes": o.SetupMin, "earliestPossible": day(v.Earliest)},
		"pushed": pushed, "newlyLate": v.NewlyLate,
		"before": fmt.Sprintf("按期 %d/%d，误期 %.1f 小时，换型 %d 次", v.Before.OnTime, v.Before.Planned, v.Before.LateHours, v.Before.Setups),
		"after":  fmt.Sprintf("按期 %d/%d，误期 %.1f 小时，换型 %d 次", v.After.OnTime, v.After.Planned, v.After.LateHours, v.After.Setups),
	}
}
