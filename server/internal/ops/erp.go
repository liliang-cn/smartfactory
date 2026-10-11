package ops

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"smartfactory/internal/auth"
	"smartfactory/internal/erp"
)

// A company's ERP connection: the ERP's own token (role erp), members to read and operators to
// send and withdraw use its orders; administrators set the connection up.

// orders lets the order API through: the company's ERP token, members to read, operators to
// send and withdraw.
func (o *Ops) orders(who auth.Who, company string, write bool) error {
	if err := o.erp(who, company, auth.Visitor); err != nil {
		return err
	}
	if !who.Orders(company, write) {
		if who.Key == "" {
			return fail(401, "用令牌登录")
		}
		if write {
			return fail(403, "发、撤工单要公司 %s 的 ERP 接口令牌或操作员以上的令牌", company)
		}
		return fail(403, "看工单要公司 %s 的 ERP 接口令牌或成员以上的令牌", company)
	}
	return nil
}

func (o *Ops) erp(who auth.Who, company string, need auth.Role) error {
	if o.Production == nil {
		return fail(503, "这台服务器没有接 ERP 的地方（没有数据目录）")
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

func erpError(err error) error {
	var p *erp.Problems
	if errors.As(err, &p) {
		return &Error{Code: 422, Msg: p.Error(), Problems: p.List}
	}
	return err
}

// ERPConfig is a connection as the console shows it: header values and the signing secret are
// never shown, only whether they are set.
type ERPConfig struct {
	erp.Config
	SecretSet bool `json:"secretSet"`
}

// ERPOverview is a company's connection, how it last went and its orders by status.
type ERPOverview struct {
	Config ERPConfig      `json:"config"`
	Status erp.Status     `json:"status"`
	Counts map[string]int `json:"counts"`
	Fields []string       `json:"fields"`
}

func masked(c erp.Config) ERPConfig {
	out := ERPConfig{Config: c}
	if c.Pull != nil {
		p := *c.Pull
		p.Headers = map[string]string{}
		for k := range c.Pull.Headers {
			p.Headers[k] = ""
		}
		out.Pull = &p
	}
	if c.Notify != nil {
		n := *c.Notify
		out.SecretSet = n.Secret != ""
		n.Secret = ""
		out.Notify = &n
	}
	return out
}

func (o *Ops) ERP(who auth.Who, company string) (*ERPOverview, error) {
	if err := o.erp(who, company, auth.Admin); err != nil {
		return nil, err
	}
	cfg, st, err := o.Production.Config(company)
	if err != nil {
		return nil, err
	}
	counts, err := o.Production.Store.Counts(company)
	if err != nil {
		return nil, err
	}
	return &ERPOverview{Config: masked(cfg), Status: st, Counts: counts, Fields: erp.Fields}, nil
}

// SetERP sets the connection up; a callback set for the first time gets its signing secret,
// returned this once.
func (o *Ops) SetERP(who auth.Who, via, company string, cfg erp.Config) (map[string]any, error) {
	if err := o.erp(who, company, auth.Admin); err != nil {
		return nil, err
	}
	secret, err := o.Production.SetConfig(company, cfg)
	detail := "停用"
	if cfg.Enabled {
		var parts []string
		if cfg.Pull != nil {
			parts = append(parts, "拉取 "+host(cfg.Pull.URL))
		}
		if cfg.Notify != nil {
			parts = append(parts, "回调 "+host(cfg.Notify.URL))
		}
		parts = append(parts, map[string]string{"idle": "没有工单时停线", "simulate": "没有工单时仿真"}[cfg.WhenEmpty])
		detail = strings.Join(parts, "，")
	}
	o.Audit.Record(who, via, company, "erp.config", company, detail, err)
	if err != nil {
		return nil, erpError(err)
	}
	out := map[string]any{"ok": true}
	if secret != "" {
		out["secret"] = secret
	}
	return out, nil
}

func host(u string) string {
	u = strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	if i := strings.IndexAny(u, "/?#"); i >= 0 {
		u = u[:i]
	}
	return u
}

// RotateERPSecret gives the callback a new signing secret, shown this once.
func (o *Ops) RotateERPSecret(who auth.Who, via, company string) (map[string]any, error) {
	if err := o.erp(who, company, auth.Admin); err != nil {
		return nil, err
	}
	secret, err := o.Production.RotateSecret(company)
	o.Audit.Record(who, via, company, "erp.secret", company, "", err)
	if err != nil {
		return nil, erpError(err)
	}
	return map[string]any{"secret": secret}, nil
}

// PullERP fetches the ERP's orders now; dry only shows what it read and where each would run.
func (o *Ops) PullERP(ctx context.Context, who auth.Who, via, company string, dry bool) (*erp.PullResult, error) {
	if err := o.erp(who, company, auth.Admin); err != nil {
		return nil, err
	}
	res, err := o.Production.Pull(ctx, company, dry)
	if !dry {
		detail := ""
		if res != nil {
			detail = fmt.Sprintf("读到 %d 张，新增 %d，更新 %d，失败 %d", res.Fetched, res.Created, res.Updated, res.Failed)
		}
		o.Audit.Record(who, via, company, "erp.pull", company, detail, err)
	}
	if err != nil {
		var p *erp.Problems
		if errors.As(err, &p) {
			return nil, erpError(err)
		}
		return nil, &Error{Code: 502, Msg: err.Error()}
	}
	return res, nil
}

// Orders are a company's production orders: running, then queued in turn, then the rest.
func (o *Ops) Orders(who auth.Who, company string, f erp.Filter) ([]erp.Order, error) {
	if err := o.orders(who, company, false); err != nil {
		return nil, err
	}
	return o.Production.Store.List(company, f)
}

func (o *Ops) Order(who auth.Who, company, no string) (*erp.Order, error) {
	if err := o.orders(who, company, false); err != nil {
		return nil, err
	}
	ord, err := o.Production.Store.Get(company, no)
	if err == nil && ord == nil {
		return nil, fail(404, "没有工单 %s", no)
	}
	return ord, err
}

// SubmitOrders stores orders sent to the twin; each is answered on its own.
func (o *Ops) SubmitOrders(who auth.Who, via, company string, raw []map[string]any) ([]erp.Result, error) {
	if err := o.orders(who, company, true); err != nil {
		return nil, err
	}
	if len(raw) == 0 || len(raw) > 1000 {
		return nil, fail(400, "一次发 1～1000 张工单")
	}
	ins := make([]erp.Input, len(raw))
	bad := make([]string, len(raw))
	for i, m := range raw {
		in, err := erp.InputFrom(m)
		ins[i] = in
		if err != nil {
			bad[i] = err.Error()
		}
	}
	var good []erp.Input
	var at []int
	for i := range ins {
		if bad[i] == "" {
			good = append(good, ins[i])
			at = append(at, i)
		}
	}
	out := make([]erp.Result, len(ins))
	for i := range ins {
		out[i] = erp.Result{No: ins[i].No, Error: bad[i]}
	}
	for j, r := range o.Production.Submit(company, good, sourceOf(via)) {
		out[at[j]] = r
	}
	var changed, failed []string
	for _, r := range out {
		switch {
		case !r.OK:
			failed = append(failed, r.No)
		case r.Outcome != erp.Unchanged:
			changed = append(changed, r.No)
		}
	}
	if len(changed) > 0 || len(failed) > 0 {
		detail := fmt.Sprintf("%d 张", len(changed))
		var err error
		if len(failed) > 0 {
			err = fmt.Errorf("%d 张没收下：%s", len(failed), strings.Join(clipList(failed, 5), "、"))
		}
		o.Audit.Record(who, via, company, "erp.orders", strings.Join(clipList(append(changed, failed...), 5), "、"), detail, err)
	}
	return out, nil
}

func sourceOf(via string) string {
	if via == "api" {
		return "push"
	}
	return "console"
}

func clipList(xs []string, n int) []string {
	if len(xs) <= n {
		return xs
	}
	return append(slices.Clone(xs[:n]), fmt.Sprintf("等 %d 张", len(xs)))
}

// CancelOrder withdraws an order; a line running it moves on.
func (o *Ops) CancelOrder(who auth.Who, via, company, no string) (*erp.Order, error) {
	if err := o.orders(who, company, true); err != nil {
		return nil, err
	}
	ord, err := o.Production.Cancel(company, no)
	if err == nil && ord == nil {
		return nil, fail(404, "没有工单 %s", no)
	}
	o.Audit.Record(who, via, company, "erp.cancel", no, "", err)
	if err != nil {
		return nil, &Error{Code: 409, Msg: err.Error()}
	}
	return ord, nil
}
