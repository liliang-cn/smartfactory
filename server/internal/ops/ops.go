// Package ops is everything the console does, once: building companies, industry packs, tokens,
// the activity log and the knowledge bases. The console's HTTP API, the command line and the
// console agent all call it, so each check and audit entry is the same whichever way a change
// comes in. Every operation takes who is asking and says what they may not do.
package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"smartfactory/internal/agents"
	"smartfactory/internal/audit"
	"smartfactory/internal/auth"
	"smartfactory/internal/calib"
	"smartfactory/internal/checks"
	"smartfactory/internal/engine"
	"smartfactory/internal/erp"
	"smartfactory/internal/guard"
	"smartfactory/internal/knowledge"
	"smartfactory/internal/mcptools"
	"smartfactory/internal/tags"
	"smartfactory/internal/templates"
)

type Ops struct {
	Engine *engine.Engine
	Keys   *auth.Keyring
	Audit  *audit.Log
	Agents *agents.Runner
	KB     *knowledge.Base
	Hub    *tags.Hub
	// Advisors is each company's advisor console, for those that have one.
	Advisors map[string]string
	Version  string
	// Saved are the templates saved on this instance.
	Saved *templates.Store
	// Production is the companies' ERP connections and production orders.
	Production *erp.Service
	// Calib fits machines' parameters from their stored readings.
	Calib *calib.Service
	// Commands is the guard in front of the companies' machines.
	Commands *guard.Guard
	// Checks are the forms people fill in by hand at the machines, and what they wrote.
	Checks *checks.Service
}

// Error is an operation refused or failed, with what an HTTP answer would say about it.
type Error struct {
	Code     int // 400 401 403 404 409 422 429 503
	Msg      string
	Problems []string
	Where    string
}

func (e *Error) Error() string { return e.Msg }

func fail(code int, format string, a ...any) error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, a...)}
}

// Admin refuses anyone without an administrator's token.
func Admin(who auth.Who) error {
	if who.Key == "" {
		return fail(401, "用管理员令牌登录")
	}
	if who.Role != auth.Admin {
		return fail(403, "%s的令牌是%s，后台要管理员令牌", who.Name, who.Role.Label())
	}
	return nil
}

func rejected(res engine.ConfigResult) error {
	return &Error{Code: 422, Msg: "配置没有通过校验：" + strings.Join(res.Errors, "；"), Problems: res.Errors, Where: res.Where}
}

func errOf(res engine.ConfigResult) error {
	if res.OK {
		return nil
	}
	return errors.New(strings.Join(res.Errors, "；"))
}

// ---- overview ----

// Company is a company as the console lists it.
type Company struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Short    string `json:"short"`
	Industry string `json:"industry"`
	// Builtin companies ship with the app; the others were created in the console or over MCP.
	Builtin bool `json:"builtin"`
	Plants  []struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Hall     string `json:"hall"`
		Site     string `json:"site"`
		Lines    int    `json:"lines"`
		Machines int    `json:"machines"`
	} `json:"plants"`
	LiveTags int  `json:"liveTags"`
	Advisor  bool `json:"advisor"`
}

type Overview struct {
	Me         auth.Who        `json:"me"`
	Companies  []Company       `json:"companies"`
	Industries json.RawMessage `json:"industries"`
	Issue      bool            `json:"issue"`
	Assistants bool            `json:"assistants"`
	Version    string          `json:"version"`
	// Runs is the companies this server is limited to; absent when any (the demo).
	Runs *[]string `json:"runs,omitempty"`
	// Single: a self-hosted instance of one company (empty until it is built)
	Single bool `json:"single"`
}

func (o *Ops) Overview(who auth.Who) (*Overview, error) {
	if err := Admin(who); err != nil {
		return nil, err
	}
	cs, err := o.Companies(who)
	if err != nil {
		return nil, err
	}
	return &Overview{Me: who, Companies: cs, Industries: json.RawMessage(o.Engine.IndustryList()), Issue: o.Keys.CanIssue(), Assistants: o.Agents != nil, Version: o.Version,
		Runs: runs(o.Engine.Runs()), Single: o.Engine.IsSingle()}, nil
}

func runs(ids []string) *[]string {
	if ids == nil {
		return nil
	}
	return &ids
}

// Companies are the companies who's token covers.
func (o *Ops) Companies(who auth.Who) ([]Company, error) {
	var list []Company
	if err := json.Unmarshal([]byte(o.Engine.List()), &list); err != nil {
		return nil, err
	}
	out := []Company{}
	for _, c := range list {
		if !who.In(c.ID) {
			continue
		}
		if o.Hub != nil {
			c.LiveTags = len(o.Hub.Last(c.ID, "", ""))
		}
		c.Advisor = o.Advisors[c.ID] != ""
		out = append(out, c)
	}
	return out, nil
}

// ---- companies ----

// CompanyConfig is a company's config as given (JSON).
func (o *Ops) CompanyConfig(who auth.Who, id string) (json.RawMessage, error) {
	if err := Admin(who); err != nil {
		return nil, err
	}
	if !who.In(id) {
		return nil, fail(403, "你的令牌不包括公司 %s", id)
	}
	js := o.Engine.CompanyConfig(id)
	if js == "null" {
		return nil, fail(404, "没有公司 %s", id)
	}
	return json.RawMessage(js), nil
}

func configID(cfg map[string]any) string { id, _ := cfg["id"].(string); return id }

// CheckCompany says what creating cfg would, without creating anything.
func (o *Ops) CheckCompany(who auth.Who, cfg map[string]any) (json.RawMessage, error) {
	if err := Admin(who); err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(cfg)
	res := o.Engine.CheckCompany(raw)
	if !res.OK {
		return nil, rejected(res)
	}
	return res.Detail, nil
}

// CreateCompany creates cfg, or replaces the company with its id (its simulation starts over).
func (o *Ops) CreateCompany(who auth.Who, via string, cfg map[string]any) (json.RawMessage, error) {
	if err := Admin(who); err != nil {
		return nil, err
	}
	id := configID(cfg)
	if !who.Can(id, auth.Admin) {
		return nil, fail(403, "新建公司要所有公司的管理员令牌；你只管理 %s", strings.Join(who.Companies, "、"))
	}
	raw, _ := json.Marshal(cfg)
	res := o.Engine.CreateCompany(raw)
	name, _ := cfg["name"].(string)
	o.Audit.Record(who, via, id, "company.create", id, name, errOf(res))
	if !res.OK {
		return nil, rejected(res)
	}
	return res.Detail, nil
}

func (o *Ops) DeleteCompany(who auth.Who, via, id string) error {
	if err := Admin(who); err != nil {
		return err
	}
	if !who.Can(id, auth.Admin) {
		return fail(403, "你不是公司 %s 的管理员", id)
	}
	res := o.Engine.DeleteCompany(id)
	o.Audit.Record(who, via, id, "company.delete", id, "", errOf(res))
	if !res.OK {
		return rejected(res)
	}
	return nil
}

// ---- industry packs ----

func (o *Ops) Industry(who auth.Who, id string) (json.RawMessage, error) {
	if err := Admin(who); err != nil {
		return nil, err
	}
	js := o.Engine.Industry(id)
	if js == "null" {
		return nil, fail(404, "没有行业包 %s", id)
	}
	return json.RawMessage(js), nil
}

// CheckIndustry says what saving an industry pack would, without saving it.
func (o *Ops) CheckIndustry(who auth.Who, cfg map[string]any) (json.RawMessage, error) {
	if err := Admin(who); err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(cfg)
	res := o.Engine.CheckIndustry(raw)
	if !res.OK {
		return nil, rejected(res)
	}
	return res.Detail, nil
}

func (o *Ops) CreateIndustry(who auth.Who, via string, cfg map[string]any) (json.RawMessage, error) {
	if err := Admin(who); err != nil {
		return nil, err
	}
	if !who.Everywhere() {
		return nil, fail(403, "行业包是所有公司共用的，要所有公司的管理员令牌")
	}
	raw, _ := json.Marshal(cfg)
	res := o.Engine.CreateIndustry(raw)
	name, _ := cfg["name"].(string)
	o.Audit.Record(who, via, "*", "industry.create", configID(cfg), name, errOf(res))
	if !res.OK {
		return nil, rejected(res)
	}
	return res.Detail, nil
}

// ---- tokens ----

func (o *Ops) Accounts(who auth.Who) ([]auth.Account, error) {
	if err := Admin(who); err != nil {
		return nil, err
	}
	return o.Keys.Accounts(who)
}

// IssueRequest asks for a token. Companies nil is every company.
type IssueRequest struct {
	Name      string   `json:"name"`
	Role      string   `json:"role"`
	Companies []string `json:"companies"`
	Expires   string   `json:"expires,omitempty"`
}

type Issued struct {
	Token   string       `json:"token"`
	Account auth.Account `json:"account"`
}

func (o *Ops) Issue(who auth.Who, via string, in IssueRequest) (*Issued, error) {
	if err := Admin(who); err != nil {
		return nil, err
	}
	role, err := auth.ParseRole(in.Role)
	if err != nil {
		return nil, fail(400, "角色要是成员（member）、操作员（operator）或管理员（admin）")
	}
	known := o.Engine.CompanyIDs()
	for _, c := range in.Companies {
		if !slices.Contains(known, c) {
			return nil, fail(400, "没有公司 %s", c)
		}
	}
	token, acc, err := o.Keys.Issue(who, in.Name, role, in.Companies, in.Expires)
	if err != nil {
		code := 400
		if errors.Is(err, auth.ErrNoStore) {
			code = 503
		}
		return nil, &Error{Code: code, Msg: err.Error()}
	}
	o.Audit.Record(who, via, scopeOf(acc.Companies), "token.issue", acc.Key+" "+acc.Name, acc.Role.String(), nil)
	return &Issued{Token: token, Account: acc}, nil
}

func (o *Ops) Revoke(who auth.Who, via, key string) error {
	if err := Admin(who); err != nil {
		return err
	}
	acc, err := o.Keys.Revoke(who, key)
	if acc.Key == "" {
		return &Error{Code: 404, Msg: err.Error()}
	}
	o.Audit.Record(who, via, scopeOf(acc.Companies), "token.revoke", acc.Key+" "+acc.Name, acc.Role.String(), err)
	if err != nil {
		return &Error{Code: 409, Msg: err.Error()}
	}
	return nil
}

// scopeOf is the company an account's audit entry belongs to: its only company, or "*".
func scopeOf(companies []string) string {
	if len(companies) == 1 {
		return companies[0]
	}
	return "*"
}

// ---- activity ----

// Activity is company's changes ("" every company's, "*" what all companies share), newest first.
func (o *Ops) Activity(ctx context.Context, who auth.Who, company string, before int64, limit int) ([]audit.Entry, error) {
	if err := Admin(who); err != nil {
		return nil, err
	}
	switch {
	case company == "" && !who.Everywhere():
		return nil, fail(403, "看所有公司的记录要所有公司的管理员令牌，选一家公司看")
	case company == "*" && !who.Everywhere():
		return nil, fail(403, "共用的改动（行业包、全局令牌）要所有公司的管理员令牌")
	case company != "" && company != "*" && !who.In(company):
		return nil, fail(403, "你的令牌不包括这家公司")
	}
	return o.Audit.List(ctx, company, before, limit)
}

// ---- the scene architect ----

// Compose has the scene architect write a company config (kind "company" or "") or an industry
// pack ("industry") from brief, changing base when given.
func (o *Ops) Compose(ctx context.Context, who auth.Who, kind, brief string, base json.RawMessage, emit func(agents.Event)) error {
	if err := Admin(who); err != nil {
		return err
	}
	if o.Agents == nil || !o.Agents.Ready() {
		return fail(503, "%s", agents.ErrNoModel)
	}
	brief = strings.TrimSpace(brief)
	if brief == "" || utf8.RuneCountInString(brief) > 4000 {
		return fail(400, "描述要 1～4000 字")
	}
	k := agents.ComposeCompany
	switch kind {
	case "", "company":
	case "industry":
		k = agents.ComposeIndustry
	default:
		return fail(400, "kind 是 company 或 industry")
	}
	return o.Agents.Compose(ctx, who, k, brief, base, mcptools.SceneGuide(), emit)
}
