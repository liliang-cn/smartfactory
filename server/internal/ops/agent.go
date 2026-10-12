package ops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	agentpkg "github.com/liliang-cn/agent-go/v3/pkg/agent"

	"smartfactory/internal/agents"
	"smartfactory/internal/auth"
	"smartfactory/internal/erp"
	"smartfactory/internal/mcptools"
)

// The console agent: an administrator asks in words — what a customer is made of, who holds
// which token, what changed yesterday, what the knowledge base says about a machine — and it
// answers with the console's own operations. It changes nothing itself: a change comes back as
// an Action for the person to confirm, and runs as them.

const consolePrompt = `你是 SmartFactory 工作台的助手，帮这家公司的管理员搭建和管理自己的智能工厂（数字孪生）。

你能做的事都通过工具：
- 看：list_customers、get_customer（配置和知识库概况）、list_industries、get_industry、list_tokens、activity（操作记录）。
- 知识库：knowledge_search（文档和工艺）、knowledge_entity（工艺图里一样东西的上下游和关系）。
- 生产工单：list_orders（ERP 下达的工单：排队、生产中、完成、撤回）、erp_status（ERP 怎么接的、上次拉取和回调怎么样）。
- 仿真校准：calibration（每台设备的模型参数：模板值、按实测拟合的值、在用的值，拟合回放的误差，最近一天模型和实测差多少）。拟合要管理员在后台点；你可以 propose_apply_calibration 提出应用拟合好的参数，说清楚哪些参数会变、误差能降多少。
- 指令把关：guard_status（AI 能对真实设备做到哪一步、规则、哪些设备接了真实设备、等待批准的指令）。
- 写配置：check_company 检查一份公司配置（不创建）；check_industry 检查一个行业包（不保存）。
- 改动：propose_create_company、propose_delete_company、propose_create_industry、propose_issue_token、propose_revoke_token、propose_rebuild_knowledge、propose_cancel_order、propose_apply_calibration、propose_approve_command、propose_deny_command。这些不会直接执行，而是作为一张待确认的卡片交给管理员，管理员点确认后才执行。提出后告诉管理员卡片上是什么，等他确认；不要说已经做了。

规则：
- 用中文，简洁。数字、编号、名称以工具返回为准，不要编。
- 建或改工厂：先 get_customer 看一个同行业的现有公司怎么写，照着写，check_company 通过后再 propose_create_company。
- 现有行业包里没有要的产品或工序：get_industry 读一个结构最像的，照着写一个新行业包（新 id，不能和已有的重复），check_industry 通过后 propose_create_industry；它保存了，公司配置才能用它。
- 发令牌：问清楚给谁、什么角色（member 成员 / operator 操作员 / admin 管理员）、哪些公司；不说就按最小权限（成员、只给提到的那家公司）。
- 删除、撤销这类改动，提出前说清后果。
- 管理员的令牌只能管它自己管的公司，工具会拒绝越权的操作，照实转告。`

// ConsoleAgent answers text in conversation session as who; emit sees the steps, every action it
// proposes ("action", in Result) and the answer.
func (o *Ops) ConsoleAgent(ctx context.Context, who auth.Who, session, text string, emit func(agents.Event)) error {
	if err := Admin(who); err != nil {
		return err
	}
	if o.Agents == nil || !o.Agents.Ready() {
		return fail(503, "%s", agents.ErrNoModel)
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return fail(400, "说点什么")
	}
	svc, err := o.Agents.NewAgent("sf-console", consolePrompt+"\n\n# 公司配置怎么写（参考）\n"+mcptools.SceneGuide())
	if err != nil {
		return err
	}
	defer svc.Close()

	type tool struct {
		name, desc string
		params     map[string]any
		run        func(m map[string]any) (any, error)
	}
	obj := func(props map[string]any, req ...string) map[string]any {
		return map[string]any{"type": "object", "properties": props, "required": req}
	}
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	s := func(m map[string]any, k string) string { v, _ := m[k].(string); return strings.TrimSpace(v) }
	raw := func(b json.RawMessage, err error) (any, error) {
		if err != nil {
			return nil, err
		}
		var v any
		return v, json.Unmarshal(b, &v)
	}
	propose := func(a Action) (any, error) {
		if err := o.describe(&a); err != nil {
			return nil, err
		}
		o.propose(who, &a)
		emit(agents.Event{Kind: "action", Result: a})
		return map[string]any{"proposed": a.Title, "note": "已交给管理员确认，确认后才会执行"}, nil
	}
	tools := []tool{
		{"list_customers", "这台服务器上的公司（一家公司自己部署的只有它自己）：工厂、产线、设备数，是否内置，实时数据点数。", obj(map[string]any{}),
			func(map[string]any) (any, error) { return o.Companies(who) }},
		{"get_customer", "一家公司的完整配置，和它知识库里有多少工艺节点、文档。", obj(map[string]any{"id": str("公司 id")}, "id"),
			func(m map[string]any) (any, error) {
				cfg, err := raw(o.CompanyConfig(who, s(m, "id")))
				if err != nil {
					return nil, err
				}
				out := map[string]any{"config": cfg}
				if st, err := o.KnowledgeStats(ctx, who, s(m, "id")); err == nil {
					out["knowledge"] = st
				}
				return out, nil
			}},
		{"list_industries", "已有的行业包：产品和设备。", obj(map[string]any{}),
			func(map[string]any) (any, error) { return raw(json.RawMessage(o.Engine.IndustryList()), nil) }},
		{"get_industry", "一个行业包的完整配置。", obj(map[string]any{"id": str("行业包 id")}, "id"),
			func(m map[string]any) (any, error) { return raw(o.Industry(who, s(m, "id"))) }},
		{"check_company", "检查一份公司配置能不能建（不创建）：通过返回各工厂的产线和设备数，不通过返回每个问题。", obj(map[string]any{"config": map[string]any{"type": "object"}}, "config"),
			func(m map[string]any) (any, error) {
				cfg, ok := m["config"].(map[string]any)
				if !ok {
					return nil, errors.New("config 要是对象")
				}
				return raw(o.CheckCompany(who, cfg))
			}},
		{"check_industry", "检查一个行业包能不能保存（不保存）：通过返回产品、设备、路线，不通过返回每个问题。", obj(map[string]any{"config": map[string]any{"type": "object"}}, "config"),
			func(m map[string]any) (any, error) {
				cfg, ok := m["config"].(map[string]any)
				if !ok {
					return nil, errors.New("config 要是对象")
				}
				return raw(o.CheckIndustry(who, cfg))
			}},
		{"propose_create_industry", "提出保存一个行业包（同 id 替换，用它的公司按新版本重建），交管理员确认。先 check_industry 通过。", obj(map[string]any{"config": map[string]any{"type": "object"}}, "config"),
			func(m map[string]any) (any, error) {
				cfg, ok := m["config"].(map[string]any)
				if !ok {
					return nil, errors.New("config 要是对象")
				}
				if _, err := o.CheckIndustry(who, cfg); err != nil {
					return nil, err
				}
				return propose(Action{Kind: "create_industry", Args: map[string]any{"config": cfg}, Detail: "保存后建工厂时可以选它"})
			}},
		{"list_tokens", "令牌：持有人、角色、公司、来源、最近使用、是否有效。", obj(map[string]any{}),
			func(map[string]any) (any, error) { return o.Accounts(who) }},
		{"activity", "操作记录，新的在前。company 不填是所有公司。", obj(map[string]any{"company": str("公司 id，可省"), "limit": map[string]any{"type": "integer"}}),
			func(m map[string]any) (any, error) {
				n, _ := m["limit"].(float64)
				return o.Activity(ctx, who, s(m, "company"), 0, max(10, min(int(n), 100)))
			}},
		{"knowledge_search", "在一家公司的知识库里搜：文档段落、工艺说明（含每个工位的上下游）和生产工单。", obj(map[string]any{"company": str("公司 id"), "query": str("问题")}, "company", "query"),
			func(m map[string]any) (any, error) {
				return o.KnowledgeSearch(ctx, who, s(m, "company"), s(m, "query"), 6)
			}},
		{"knowledge_entity", "知识图谱里一样东西（工位编号、设备名、产线、产品、供应商、客户、工单号）和它的关系。", obj(map[string]any{"company": str("公司 id"), "name": str("名字")}, "company", "name"),
			func(m map[string]any) (any, error) { return o.KnowledgeEntity(ctx, who, s(m, "company"), s(m, "name")) }},
		{"list_orders", "一家公司的生产工单（来自 ERP）：在做的、排队的（按将要生产的顺序）、完成的和撤回的，含产线、完成数、交期、客户。", obj(map[string]any{
			"company": str("公司 id"), "status": str("queued、running、done、cancelled，逗号分隔，可省"), "q": str("按工单号、料号、客户、产线找，可省"),
		}, "company"),
			func(m map[string]any) (any, error) {
				var st []string
				for _, x := range strings.Split(s(m, "status"), ",") {
					if x = strings.TrimSpace(x); x != "" {
						st = append(st, x)
					}
				}
				return o.Orders(who, s(m, "company"), erp.Filter{Status: st, Q: s(m, "q"), Limit: 50})
			}},
		{"erp_status", "一家公司的 ERP 对接：开没开、没工单时停线还是仿真、拉取地址和上次结果、回调成功失败数、各状态工单数。", obj(map[string]any{"company": str("公司 id")}, "company"),
			func(m map[string]any) (any, error) { return o.ERP(who, s(m, "company")) }},
		{"calibration", "一家公司每台设备的仿真参数：模板值、按实测拟合的值（附依据的读数个数和可信度，拟合不了的说明缺什么）、在用的值；拟合期间回放的误差（模板 vs 拟合）；最近一天模型和实测差多少。只列有实测数据或在用校准的设备。", obj(map[string]any{"company": str("公司 id")}, "company"),
			func(m map[string]any) (any, error) {
				all, err := o.Calibration(who, s(m, "company"))
				if err != nil {
					return nil, err
				}
				var out []any
				for _, x := range all {
					if x.Stored || x.AppliedAt != nil || len(x.Residual) > 0 {
						out = append(out, x)
					}
				}
				if out == nil {
					return map[string]any{"machines": []any{}, "note": "没有设备上报过实测数据，没有可校准的"}, nil
				}
				return map[string]any{"machines": out}, nil
			}},
		{"propose_apply_calibration", "提出应用拟合好的仿真参数（只应用拟合出来的那些），交管理员确认。machines 不给就是所有拟合过的设备。", obj(map[string]any{
			"company":  str("公司 id"),
			"machines": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "设备编号，如 HP-101，或 plant/asset；可省"},
		}, "company"),
			func(m map[string]any) (any, error) {
				args := map[string]any{"company": s(m, "company")}
				if ms, ok := m["machines"].([]any); ok && len(ms) > 0 {
					args["machines"] = ms
				}
				return propose(Action{Kind: "apply_calibration", Args: args, Detail: "仿真改用拟合的参数，推演也按它算；随时可以在后台撤销"})
			}},
		{"guard_status", "一家公司的指令把关：等级（advisory 只提议 / guarded 列出的要批准 / autonomous 只看规则）、规则、接了真实设备的机器、等待批准的指令（编号、谁发的、设备、指令、为什么等、当时设备状态）。", obj(map[string]any{"company": str("公司 id")}, "company"),
			func(m map[string]any) (any, error) {
				ov, err := o.Guard(who, s(m, "company"))
				if err != nil {
					return nil, err
				}
				waiting, err := o.CommandRequests(who, s(m, "company"), "waiting")
				return map[string]any{"guard": ov, "waiting": waiting}, err
			}},
		{"propose_approve_command", "提出批准一条等待中的指令（会按批准时设备的状态再过一遍规则，然后下到设备），交管理员确认。", obj(map[string]any{"company": str("公司 id"), "id": str("指令编号 CQ-…")}, "company", "id"),
			func(m map[string]any) (any, error) {
				return propose(Action{Kind: "approve_command", Args: map[string]any{"company": s(m, "company"), "id": s(m, "id")}, Detail: "指令会下到真实设备"})
			}},
		{"propose_deny_command", "提出拒绝一条等待中的指令，交管理员确认。", obj(map[string]any{"company": str("公司 id"), "id": str("指令编号 CQ-…"), "reason": str("理由，可省")}, "company", "id"),
			func(m map[string]any) (any, error) {
				return propose(Action{Kind: "deny_command", Args: map[string]any{"company": s(m, "company"), "id": s(m, "id"), "reason": s(m, "reason")}})
			}},
		{"propose_cancel_order", "提出撤回一张工单（在做的产线会停下它转做下一张），交管理员确认。", obj(map[string]any{"company": str("公司 id"), "no": str("工单号")}, "company", "no"),
			func(m map[string]any) (any, error) {
				return propose(Action{Kind: "cancel_order", Args: map[string]any{"company": s(m, "company"), "no": s(m, "no")}, Detail: "在做的产线停下它，转做下一张；ERP 再发一次同号工单会重新排队"})
			}},
		{"propose_create_company", "提出新建（或同 id 替换）一家公司的工厂，交管理员确认。先 check_company 通过。", obj(map[string]any{"config": map[string]any{"type": "object"}}, "config"),
			func(m map[string]any) (any, error) {
				cfg, ok := m["config"].(map[string]any)
				if !ok {
					return nil, errors.New("config 要是对象")
				}
				if _, err := o.CheckCompany(who, cfg); err != nil {
					return nil, err
				}
				return propose(Action{Kind: "create_company", Args: map[string]any{"config": cfg}, Detail: "立即开始仿真"})
			}},
		{"propose_delete_company", "提出删除一家运行时建的公司，交管理员确认。", obj(map[string]any{"id": str("公司 id")}, "id"),
			func(m map[string]any) (any, error) {
				return propose(Action{Kind: "delete_company", Args: map[string]any{"id": s(m, "id")}, Detail: "停止仿真、删除配置；助手、资料和记录保留"})
			}},
		{"propose_issue_token", "提出发一个令牌，交管理员确认。", obj(map[string]any{
			"name": str("持有人"), "role": str("member | operator | admin"),
			"companies": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "公司 id；空数组不行，所有公司传 null"},
			"expires":   str("最后一天 YYYY-MM-DD，可省"),
		}, "name", "role"),
			func(m map[string]any) (any, error) {
				args := map[string]any{"name": s(m, "name"), "role": s(m, "role"), "companies": m["companies"]}
				if e := s(m, "expires"); e != "" {
					args["expires"] = e
				}
				return propose(Action{Kind: "issue_token", Args: args, Detail: "令牌只在确认后显示一次"})
			}},
		{"propose_revoke_token", "提出撤销一个令牌（用 list_tokens 里的 key），交管理员确认。", obj(map[string]any{"key": str("令牌 key")}, "key"),
			func(m map[string]any) (any, error) {
				return propose(Action{Kind: "revoke_token", Args: map[string]any{"key": s(m, "key")}, Detail: "立即失效"})
			}},
		{"propose_rebuild_knowledge", "提出重建一家公司的知识库（工艺图和文档重新写入），交管理员确认。", obj(map[string]any{"company": str("公司 id")}, "company"),
			func(m map[string]any) (any, error) {
				return propose(Action{Kind: "rebuild_knowledge", Args: map[string]any{"company": s(m, "company")}})
			}},
	}
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.name
		run := t.run
		svc.AddToolWithMetadata(t.name, t.desc, t.params, func(_ context.Context, m map[string]any) (any, error) {
			v, err := run(m)
			var oe *Error
			if errors.As(err, &oe) {
				return nil, fmt.Errorf("%s", oe.Msg+problems(oe))
			}
			return v, err
		}, agentpkg.ToolMetadata{ReadOnly: true, ConcurrencySafe: !strings.HasPrefix(t.name, "propose_")})
	}
	goal := fmt.Sprintf("（管理员：%s，%s）\n\n%s", who.Name, scopeText(who), text)
	_, err = agents.Run(auth.With(ctx, who), svc, goal, session, 24, names, emit)
	return err
}

func problems(e *Error) string {
	if len(e.Problems) == 0 {
		return ""
	}
	return "\n- " + strings.Join(e.Problems, "\n- ")
}

func scopeText(w auth.Who) string {
	if w.Everywhere() {
		return "管理所有公司"
	}
	return "管理 " + strings.Join(w.Companies, "、")
}
