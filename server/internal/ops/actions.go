package ops

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"smartfactory/internal/auth"
	"smartfactory/internal/guard"
)

// An Action is a change the console agent proposes and a person carries out: the page shows it
// with a button, the command line asks y/N, and either runs it here as the person who confirmed.
type Action struct {
	// ID names a proposal of the console agent (AP-…), so what became of it can be counted;
	// an action that was never proposed has none.
	ID string `json:"id,omitempty"`
	// Kind: create_company | delete_company | create_industry | issue_token | revoke_token | rebuild_knowledge | cancel_order | apply_calibration | approve_command | deny_command
	Kind string `json:"kind"`
	// Title is the change in a few words; Detail what it does.
	Title  string         `json:"title"`
	Detail string         `json:"detail,omitempty"`
	Args   map[string]any `json:"args"`
}

// Do carries out an action as who. A proposal's outcome goes into the activity log as
// agent.confirm, beside the change's own entry.
func (o *Ops) Do(ctx context.Context, who auth.Who, via string, a Action) (any, error) {
	v, err := o.do(ctx, who, via, a)
	if a.ID != "" && who.Key != "" {
		o.Audit.Record(who, via, actionCompany(a), "agent.confirm", a.ID, a.Kind, err)
	}
	return v, err
}

// Dismiss records that who turned a proposal down.
func (o *Ops) Dismiss(who auth.Who, via string, a Action) error {
	if err := Admin(who); err != nil {
		return err
	}
	if !strings.HasPrefix(a.ID, "AP-") {
		return fail(400, "没有这条提议的编号")
	}
	o.Audit.Record(who, via, actionCompany(a), "agent.dismiss", a.ID, a.Kind, nil)
	return nil
}

// propose names an action the console agent proposes and records it, so the value report can
// say how many were confirmed and how many turned down.
func (o *Ops) propose(who auth.Who, a *Action) {
	b := make([]byte, 4)
	rand.Read(b)
	a.ID = "AP-" + strings.ToUpper(hex.EncodeToString(b))
	o.Audit.Record(who, "assistant", actionCompany(*a), "agent.propose", a.ID, a.Kind+" "+a.Title, nil)
}

// actionCompany is the company an action is about, as its activity entries file it: "*" when
// it is about none or several.
func actionCompany(a Action) string {
	str := func(k string) string { s, _ := a.Args[k].(string); return s }
	switch a.Kind {
	case "create_company":
		cfg, _ := a.Args["config"].(map[string]any)
		return cmp.Or(configID(cfg), "*")
	case "delete_company":
		return cmp.Or(str("id"), "*")
	case "issue_token":
		return scopeOf(strs(a.Args["companies"]))
	case "create_industry", "revoke_token":
		return "*"
	}
	return cmp.Or(str("company"), "*")
}

func (o *Ops) do(ctx context.Context, who auth.Who, via string, a Action) (any, error) {
	str := func(k string) string { s, _ := a.Args[k].(string); return s }
	switch a.Kind {
	case "create_company":
		cfg, ok := a.Args["config"].(map[string]any)
		if !ok {
			return nil, fail(400, "create_company 要 config")
		}
		return o.CreateCompany(who, via, cfg)
	case "delete_company":
		return map[string]bool{"ok": true}, o.DeleteCompany(who, via, str("id"))
	case "create_industry":
		cfg, ok := a.Args["config"].(map[string]any)
		if !ok {
			return nil, fail(400, "create_industry 要 config")
		}
		return o.CreateIndustry(who, via, cfg)
	case "issue_token":
		var in IssueRequest
		b, _ := json.Marshal(a.Args)
		if err := json.Unmarshal(b, &in); err != nil {
			return nil, fail(400, "issue_token：%v", err)
		}
		return o.Issue(who, via, in)
	case "revoke_token":
		return map[string]bool{"ok": true}, o.Revoke(who, via, str("key"))
	case "rebuild_knowledge":
		return o.KnowledgeRebuild(ctx, who, via, str("company"))
	case "cancel_order":
		return o.CancelOrder(who, via, str("company"), str("no"))
	case "apply_calibration":
		return o.ApplyCalibration(who, via, str("company"), strs(a.Args["machines"]))
	case "approve_command":
		return o.ApproveCommand(who, via, str("company"), str("id"))
	case "deny_command":
		return o.DenyCommand(who, via, str("company"), str("id"), str("reason"))
	}
	return nil, fail(400, "没有这种操作：%s", a.Kind)
}

// describe is how an action reads in the console and on the command line.
func (o *Ops) describe(a *Action) error {
	names := map[string]string{}
	var list []struct{ ID, Name string }
	if json.Unmarshal([]byte(o.Engine.List()), &list) == nil {
		for _, c := range list {
			names[c.ID] = c.Name
		}
	}
	named := func(id string) string {
		if n, ok := names[id]; ok {
			return n
		}
		return id
	}
	str := func(k string) string { s, _ := a.Args[k].(string); return s }
	switch a.Kind {
	case "create_company":
		cfg, _ := a.Args["config"].(map[string]any)
		name, _ := cfg["name"].(string)
		id, _ := cfg["id"].(string)
		a.Title = fmt.Sprintf("建立公司 %s（%s）", name, id)
	case "delete_company":
		a.Title = "删除公司 " + named(str("id"))
	case "create_industry":
		cfg, _ := a.Args["config"].(map[string]any)
		a.Title = fmt.Sprintf("保存行业包 %v（%v）", cfg["name"], cfg["id"])
	case "issue_token":
		role, _ := auth.ParseRole(str("role"))
		scope := "所有公司"
		if cs, ok := a.Args["companies"].([]any); ok && len(cs) > 0 {
			names := make([]string, len(cs))
			for i, c := range cs {
				names[i] = named(fmt.Sprint(c))
			}
			scope = strings.Join(names, "、")
		}
		a.Title = fmt.Sprintf("给%s发%s令牌（%s）", str("name"), role.Label(), scope)
	case "revoke_token":
		a.Title = "撤销令牌 " + str("key")
	case "rebuild_knowledge":
		a.Title = "重建" + named(str("company")) + "的知识库"
	case "cancel_order":
		a.Title = "撤回工单 " + str("no")
	case "apply_calibration":
		ms := strs(a.Args["machines"])
		which := "所有拟合过的设备"
		if len(ms) > 0 {
			which = strings.Join(ms, "、")
		}
		a.Title = fmt.Sprintf("应用%s %s的校准参数", named(str("company")), which)
	case "approve_command", "deny_command":
		verb := "批准"
		if a.Kind == "deny_command" {
			verb = "拒绝"
		}
		a.Title = verb + "指令 " + str("id")
		if o.Commands != nil {
			if r, _ := o.Commands.Get(str("company"), str("id")); r != nil {
				a.Title = fmt.Sprintf("%s %s 对 %s 的「%s」", verb, r.Who, r.Asset, guard.Text(r.Cmd, r.Arg))
			}
		}
	default:
		return fail(400, "没有这种操作：%s", a.Kind)
	}
	return nil
}

// strs reads a list of strings from an action's arguments.
func strs(v any) []string {
	var out []string
	switch x := v.(type) {
	case []any:
		for _, e := range x {
			if s, ok := e.(string); ok && s != "" {
				out = append(out, s)
			}
		}
	case []string:
		out = x
	}
	return out
}
