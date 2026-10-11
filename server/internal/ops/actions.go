package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"smartfactory/internal/auth"
)

// An Action is a change the console agent proposes and a person carries out: the page shows it
// with a button, the command line asks y/N, and either runs it here as the person who confirmed.
type Action struct {
	// Kind: create_company | delete_company | create_industry | issue_token | revoke_token | rebuild_knowledge | cancel_order
	Kind string `json:"kind"`
	// Title is the change in a few words; Detail what it does.
	Title  string         `json:"title"`
	Detail string         `json:"detail,omitempty"`
	Args   map[string]any `json:"args"`
}

// Do carries out an action as who.
func (o *Ops) Do(ctx context.Context, who auth.Who, via string, a Action) (any, error) {
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
	default:
		return fail(400, "没有这种操作：%s", a.Kind)
	}
	return nil
}
