package agents

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/google/uuid"
	agentpkg "github.com/liliang-cn/agent-go/v3/pkg/agent"

	"smartfactory/internal/auth"
)

// The scene architect: in the console, an administrator describes a customer's site and it
// writes the company config from the industry packs there are, checking each attempt with the
// engine (nothing is created: the console shows the plans and the administrator builds it).

const architectPrompt = `你是 SmartFactory 的场景搭建员。管理员用几句话描述一个客户的工厂（或营地、餐厅……），你把它写成一份公司配置（company），给管理员在后台确认后建成数字孪生。

做法：
1. list_industries 看有哪些行业包；挑最贴近客户的一个。只能用已有的行业包和里面的产品、设备、工艺路线，不要编造。
2. list_companies / get_company 看现成的公司怎么写产线（product + route 的组合、repeat、名字），照着写最稳；get_industry 看行业包里的产品和路线。
3. 按描述定工厂（plants）和每个工厂的产线（lines）：工厂 id 用 p1、p2…，name 像「一厂」「常州工厂」，hall 写车间名，badge 两三个字；同样的线用 repeat 加 {n}，比如 "曲轴 {n} 线"。
4. 客户 id 用客户名的拼音或英文缩写，小写字母、数字、连字符；short 是 2～4 个字的简称；name 是全称。
5. 每写好一版就调 propose_company 检查；没通过就按它说的改，直到通过。通过的那一版会出现在管理员的表单里。
6. 描述里没说的（车间名、几条线）按行业常见的规模定，在最后的回答里说出你替客户做的假设。描述要的东西行业包里没有，就用最接近的，并说清楚缺什么。

最后用两三句中文告诉管理员：建了什么（几个工厂、几条线、生产什么），以及你做了哪些假设。不要贴 JSON。`

// Compose writes a company config from brief, starting from base (the form's current config,
// may be empty); emit sees the steps, every config that passed the check ("draft") and the answer.
func (r *Runner) Compose(ctx context.Context, who auth.Who, brief string, base json.RawMessage, guide string, emit func(Event)) error {
	if r.llm == nil {
		return ErrNoModel
	}
	svc, err := agentpkg.New("sf-architect").WithLLM(r.llm).WithSystemPrompt(architectPrompt + "\n\n# 配置怎么写（参考）\n" + guide).
		WithDBPath(filepath.Join(r.dir, "agentgo.db")).WithTimezone(shanghai).Build()
	if err != nil {
		return err
	}
	defer svc.Close()
	eng := r.Eng
	var proposed json.RawMessage
	baseID := ""
	if len(base) > 0 {
		var b struct{ ID string }
		json.Unmarshal(base, &b)
		baseID = b.ID
	}
	type tl struct {
		name, desc string
		params     map[string]any
		run        func(args) (any, error)
	}
	obj := func(props map[string]any, req ...string) map[string]any {
		return map[string]any{"type": "object", "properties": props, "required": req}
	}
	idParam := obj(map[string]any{"id": map[string]any{"type": "string"}}, "id")
	raw := func(js string) (any, error) {
		var v any
		if err := json.Unmarshal([]byte(js), &v); err != nil {
			return nil, err
		}
		return v, nil
	}
	tools := []tl{
		{"list_industries", "已有的行业包：每个的产品和设备。", obj(map[string]any{}), func(args) (any, error) { return raw(eng.IndustryList()) }},
		{"get_industry", "一个行业包的完整配置：产品、设备、工艺路线（routes）。", idParam, func(a args) (any, error) {
			js := eng.Industry(a.str("id"))
			if js == "null" {
				return nil, fmt.Errorf("没有行业包 %q，先 list_industries", a.str("id"))
			}
			return raw(js)
		}},
		{"list_companies", "现有的公司：工厂、产线数和设备数。", obj(map[string]any{}), func(args) (any, error) { return raw(eng.List()) }},
		{"get_company", "一家现有公司的完整配置，照着写最稳。", idParam, func(a args) (any, error) {
			id := a.str("id")
			if !who.In(id) {
				return nil, fmt.Errorf("看不到公司 %q", id)
			}
			js := eng.CompanyConfig(id)
			if js == "null" {
				return nil, fmt.Errorf("没有公司 %q，先 list_companies", id)
			}
			return raw(js)
		}},
		{"propose_company", "检查一份公司配置（不会创建）。通过了就放进管理员的表单；没通过返回每个问题。", obj(map[string]any{
			"config": map[string]any{"type": "object", "description": "完整的公司配置：id、name、short、industry、plants[{id,name,hall,badge,lines[{name,product,route,repeat,note}]}]"},
		}, "config"), func(a args) (any, error) {
			cfg, ok := a["config"].(map[string]any)
			if !ok {
				return nil, errors.New("config 要是一个对象")
			}
			b, _ := json.Marshal(cfg)
			id, _ := cfg["id"].(string)
			if id != baseID && slices.Contains(eng.CompanyIDs(), id) {
				return nil, fmt.Errorf("已经有公司叫 %q 了，换一个 id", id)
			}
			res := eng.CheckCompany(b)
			if !res.OK {
				return nil, fmt.Errorf("没通过：\n- %s", strings.Join(res.Errors, "\n- "))
			}
			proposed = b
			var draft map[string]any
			json.Unmarshal(b, &draft)
			emit(Event{Kind: "draft", Result: draft})
			return res.Detail, nil
		}},
	}
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.name
		run := t.run
		svc.AddToolWithMetadata(t.name, t.desc, t.params, func(_ context.Context, m map[string]any) (any, error) { return run(args(m)) },
			agentpkg.ToolMetadata{ReadOnly: true, ConcurrencySafe: t.name != "propose_company"})
	}

	goal := "客户的描述：\n" + brief
	if len(base) > 0 {
		goal += "\n\n管理员表单里现在是这份配置，在它上面按描述改（id 不变，除非描述要求）：\n" + string(base)
	}
	ch, err := svc.RunStreamWithOptions(auth.With(ctx, who), goal, agentpkg.WithSessionID(uuid.NewString()), agentpkg.WithMaxTurns(20))
	if err != nil {
		return err
	}
	var answer, partial, failure string
	for ev := range ch {
		if (ev.Type == agentpkg.EventTypeToolCall || ev.Type == agentpkg.EventTypeToolResult) && !slices.Contains(names, ev.ToolName) {
			continue
		}
		switch ev.Type {
		case agentpkg.EventTypeThinking:
			emit(Event{Kind: "thinking"})
		case agentpkg.EventTypeToolCall:
			args := ev.ToolArgs
			if ev.ToolName == "propose_company" {
				args = nil // the config itself arrives as a draft
			}
			emit(Event{Kind: "tool_call", Tool: ev.ToolName, Args: args})
		case agentpkg.EventTypeToolResult:
			emit(Event{Kind: "tool_result", Tool: ev.ToolName, Result: summary(ev.ToolResult)})
		case agentpkg.EventTypePartial:
			partial += ev.Content
			emit(Event{Kind: "delta", Text: ev.Content})
		case agentpkg.EventTypeTombstone:
			partial = ""
			emit(Event{Kind: "reset"})
		case agentpkg.EventTypeComplete:
			answer = ev.Content
		case agentpkg.EventTypeError:
			failure = ev.Content
		}
	}
	if answer == "" {
		answer = partial
	}
	switch {
	case ctx.Err() != nil:
		return nil
	case proposed == nil:
		if answer == "" {
			answer = failure
		}
		emit(Event{Kind: "error", Text: cmp.Or(strings.TrimSpace(answer), "没有写出能通过检查的配置")})
	default:
		emit(Event{Kind: "complete", Text: answer})
	}
	return nil
}

// summary keeps what the console shows of a tool's result: errors whole, the rest short.
func summary(v any) any {
	if s, ok := v.(string); ok && len(s) > 600 {
		return s[:600] + "…"
	}
	return compact(v)
}
