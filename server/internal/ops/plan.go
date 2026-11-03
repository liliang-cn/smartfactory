package ops

import (
	"errors"
	"fmt"
	"slices"
	"time"

	"smartfactory/internal/auth"
	"smartfactory/internal/plan"
)

// A company's production plan (生产计划): members read it, operators and above (the planners)
// re-plan, move, lock and publish it. Publishing is in the activity log.

func (o *Ops) plans(who auth.Who, company string, need auth.Role) error {
	if o.Plans == nil {
		return fail(503, "这台服务器没有排产的地方（没有数据目录）")
	}
	if !slices.Contains(o.Engine.CompanyIDs(), company) {
		return fail(404, "没有公司 %s", company)
	}
	if !who.Can(company, need) {
		if who.Key == "" {
			return fail(401, "用令牌登录")
		}
		if need >= auth.Operator {
			return fail(403, "排计划、发布要公司 %s 操作员以上的账号（计划员）", company)
		}
		return fail(403, "看生产计划要公司 %s %s以上的账号", company, need.Label())
	}
	return nil
}

func planError(err error) error {
	var p *plan.Problems
	if errors.As(err, &p) {
		return &Error{Code: 422, Msg: p.Error(), Problems: p.List}
	}
	return err
}

// PlanOverview is the planning page: the plan, the draft, how they differ, what changed, and
// whether the caller may change it.
type PlanOverview struct {
	*plan.Overview
	Edit bool `json:"edit"`
}

func (o *Ops) PlanOverview(who auth.Who, company string) (*PlanOverview, error) {
	if err := o.plans(who, company, auth.Member); err != nil {
		return nil, err
	}
	ov, err := o.Plans.Overview(company)
	if err != nil {
		return nil, planError(err)
	}
	return &PlanOverview{Overview: ov, Edit: who.Can(company, auth.Operator)}, nil
}

// Replan makes a new draft for a goal ("" keeps the company's).
func (o *Ops) Replan(who auth.Who, company string, goal plan.Goal) (*plan.Plan, error) {
	if err := o.plans(who, company, auth.Operator); err != nil {
		return nil, err
	}
	p, err := o.Plans.Replan(company, goal, who.Name)
	return p, planError(err)
}

// MovePlanned puts an order on a line at a place in the draft.
func (o *Ops) MovePlanned(who auth.Who, company, no, line string, at int) (*plan.Plan, error) {
	if err := o.plans(who, company, auth.Operator); err != nil {
		return nil, err
	}
	p, err := o.Plans.Move(company, no, line, at, who.Name)
	return p, planError(err)
}

// LockPlanned keeps an order where the draft has it, or lets it go.
func (o *Ops) LockPlanned(who auth.Who, via, company, no string, on bool) (*plan.Plan, error) {
	if err := o.plans(who, company, auth.Operator); err != nil {
		return nil, err
	}
	p, err := o.Plans.Lock(company, no, on, who.Name)
	detail := "锁定"
	if !on {
		detail = "解锁"
	}
	o.Audit.Record(who, via, company, "plan.lock", no, detail, err)
	return p, planError(err)
}

// SetPlanSettings changes how the company plans.
func (o *Ops) SetPlanSettings(who auth.Who, via, company string, st plan.Settings) (plan.Settings, error) {
	if err := o.plans(who, company, auth.Operator); err != nil {
		return st, err
	}
	out, err := o.Plans.SetSettings(company, st)
	detail := fmt.Sprintf("%s，缺料%s，%d 个预计到料，%d 个停线计划", plan.GoalLabel[out.Goal], map[string]string{plan.Hold: "先不排", plan.AsIs: "也照排"}[out.Material], len(out.Arrive), len(out.Stops))
	o.Audit.Record(who, via, company, "plan.settings", company, detail, err)
	return out, planError(err)
}

func (o *Ops) DiscardPlan(who auth.Who, company string) error {
	if err := o.plans(who, company, auth.Operator); err != nil {
		return err
	}
	return o.Plans.Discard(company)
}

// PublishPlan makes the draft the plan in force: the lines run the orders in its order.
func (o *Ops) PublishPlan(who auth.Who, via, company string) (*plan.PublishResult, error) {
	if err := o.plans(who, company, auth.Operator); err != nil {
		return nil, err
	}
	res, err := o.Plans.Publish(company, who.Name)
	target, detail := company, ""
	if res != nil {
		k := res.Plan.Result.KPI
		target = res.Plan.ID
		detail = fmt.Sprintf("%s：%d 张，按期 %.0f%%，换型 %d 次；%s", plan.GoalLabel[res.Plan.Goal], k.Planned, k.OnTimePct, k.Setups, res.Diff.Text)
	}
	o.Audit.Record(who, via, company, "plan.publish", target, detail, err)
	return res, planError(err)
}

func (o *Ops) PlanHistory(who auth.Who, company string) ([]plan.Plan, error) {
	if err := o.plans(who, company, auth.Member); err != nil {
		return nil, err
	}
	return o.Plans.History(company)
}

// PlanWhatIf starts checking the draft or the plan in force on the simulation.
func (o *Ops) PlanWhatIf(who auth.Who, company, which string, days float64) (plan.WhatIf, error) {
	if err := o.plans(who, company, auth.Member); err != nil {
		return plan.WhatIf{}, err
	}
	w, err := o.Plans.StartWhatIf(company, which, days)
	return w, planError(err)
}

func (o *Ops) PlanWhatIfStatus(who auth.Who, company string) (*plan.WhatIf, error) {
	if err := o.plans(who, company, auth.Member); err != nil {
		return nil, err
	}
	return o.Plans.WhatIfStatus(company), nil
}

// PlanRush is what an urgent order would do to the plan in force.
func (o *Ops) PlanRush(who auth.Who, company string, r plan.RushInput) (*plan.Impact, error) {
	if err := o.plans(who, company, auth.Member); err != nil {
		return nil, err
	}
	if r.Due.IsZero() {
		r.Due = time.Now().Add(72 * time.Hour)
	}
	v, err := o.Plans.Rush(company, r)
	return v, planError(err)
}

func (o *Ops) PlanStatus(who auth.Who, company string) (map[string]any, error) {
	if err := o.plans(who, company, auth.Member); err != nil {
		return nil, err
	}
	v, err := o.Plans.Status(company)
	return v, planError(err)
}
