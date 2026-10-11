package ops

import (
	"errors"
	"slices"

	"smartfactory/internal/auth"
	"smartfactory/internal/guard"
)

// A company's command guard: operators see it and decide on the commands that wait for a
// person, administrators set it.

func (o *Ops) guard(who auth.Who, company string, need auth.Role) error {
	if o.Commands == nil {
		return fail(503, "这台服务器没有指令把关（没有数据目录）")
	}
	if !slices.Contains(o.Engine.CompanyIDs(), company) {
		return fail(404, "没有公司 %s", company)
	}
	if !who.Can(company, need) {
		if who.Key == "" {
			return fail(401, "用令牌登录")
		}
		return fail(403, "要公司 %s %s以上的令牌", company, need.Label())
	}
	return nil
}

// GuardOverview is a company's guard as the console shows it.
type GuardOverview struct {
	Settings guard.Settings `json:"settings"`
	Defaults guard.Settings `json:"defaults"`
	Commands []string       `json:"commands"`
	// Machines are the company's machines (plant/asset) fed by real equipment: their commands
	// are judged.
	Machines []string `json:"machines"`
	// Waiting is how many commands wait for a person.
	Waiting int `json:"waiting"`
	// Admin: the caller may change the settings.
	Admin bool `json:"admin"`
}

func (o *Ops) Guard(who auth.Who, company string) (*GuardOverview, error) {
	if err := o.guard(who, company, auth.Operator); err != nil {
		return nil, err
	}
	waiting, err := o.Commands.List(company, "waiting", 500)
	if err != nil {
		return nil, err
	}
	return &GuardOverview{Settings: o.Commands.Settings(company), Defaults: guard.Defaults(), Commands: guard.Commands,
		Machines: o.Commands.Machines(company), Waiting: len(waiting), Admin: who.Can(company, auth.Admin)}, nil
}

// SetGuard changes a company's guard.
func (o *Ops) SetGuard(who auth.Who, via, company string, s guard.Settings) (*GuardOverview, error) {
	if err := o.guard(who, company, auth.Admin); err != nil {
		return nil, err
	}
	err := o.Commands.SetSettings(company, s)
	o.Audit.Record(who, via, company, "guard.config", company, string(s.Level), err)
	var p *guard.Problems
	if errors.As(err, &p) {
		return nil, &Error{Code: 422, Msg: p.Error(), Problems: p.List}
	}
	if err != nil {
		return nil, err
	}
	return o.Guard(who, company)
}

// CommandRequests are a company's commands that waited for a person, waiting ones first.
func (o *Ops) CommandRequests(who auth.Who, company, status string) ([]guard.Request, error) {
	if err := o.guard(who, company, auth.Operator); err != nil {
		return nil, err
	}
	return o.Commands.List(company, status, 200)
}

// ApproveCommand carries a waiting command out, as who.
func (o *Ops) ApproveCommand(who auth.Who, via, company, id string) (*guard.Request, error) {
	if err := o.guard(who, company, auth.Operator); err != nil {
		return nil, err
	}
	return decided(o.Commands.Approve(who, via, company, id))
}

// DenyCommand turns a waiting command down.
func (o *Ops) DenyCommand(who auth.Who, via, company, id, reason string) (*guard.Request, error) {
	if err := o.guard(who, company, auth.Operator); err != nil {
		return nil, err
	}
	return decided(o.Commands.Deny(who, via, company, id, reason))
}

func decided(r *guard.Request, err error) (*guard.Request, error) {
	switch {
	case errors.Is(err, guard.ErrDecided):
		return nil, &Error{Code: 409, Msg: "这条指令已经是「" + StatusLabel[r.Status] + "」了"}
	case err != nil:
		return nil, err
	case r == nil:
		return nil, fail(404, "没有这条指令")
	}
	return r, nil
}

// StatusLabel is how a request's status reads.
var StatusLabel = map[string]string{
	"waiting": "等待批准", "deciding": "正在处理", "sent": "已执行", "refused": "被规则拒绝", "denied": "已拒绝", "expired": "已过期", "failed": "执行失败",
}
