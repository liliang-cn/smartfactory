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
	"smartfactory/internal/engine"
)

// The scene architect: in the console, an administrator describes their site and it writes the
// company config — from the industry packs there are, or with a new pack when none fits — or a
// pack alone, checking each attempt with the engine (nothing is created: the console shows the
// plans and the administrator builds them).

const architectPrompt = `你是 SmartFactory 的场景搭建员。管理员用几句话描述自己的工厂（或营地、餐厅……），你把它写成配置，给管理员在后台确认后建成数字孪生。

做法：
1. list_industries 看有哪些行业包；挑最贴近的一个。get_industry 看它的产品、设备、工艺路线（routes）。
2. 描述里的产品或工序，现有行业包里没有（比如要做注塑件、制药、烘焙，而行业包只有锻造、食品……），就写一个新的行业包：
   - 先 get_industry 读一个结构最像的（离散制造看 forging 或 machining，流程/包装看 food，人或车走动的服务场景看 flow 为 walk 的），照它的结构写；
   - id 用英文小写和连字符，不能和 list_industries 里已有的重复；name 是中文行业名；
   - 产品 1～3 个，每个 2～4 个阶段（stages，每个阶段用几个 parts 画出样子）；设备 4～10 种，每种选合适的 look.archetype，cycle、power、thermal 按行业常识；每个产品一条 routes，3～8 步，makes 指向后面的阶段；
   - 有料号就写 partNo（或 variants），有客户、供应商就写 customers、suppliers，这样工单、质量追溯和知识库都能用上；
   - propose_industry 检查；没通过就按它说的改，直到通过（通过一次就够，不要重复提交）。通过后它会放进管理员那边，建公司时一起装上。
3. list_companies / get_company 看现成的公司怎么写产线（product + route 的组合、repeat、名字），照着写最稳。
4. 按描述定工厂（plants）和每个工厂的产线（lines）：工厂 id 用 p1、p2…，name 像「一厂」「常州工厂」，hall 写车间名，badge 两三个字；同样的线用 repeat 加 {n}，比如 "曲轴 {n} 线"。
5. 公司 id 用公司名的拼音或英文缩写，小写字母、数字、连字符；short 是 2～4 个字的简称；name 是全称。
6. 每写好一版就调 propose_company 检查（用新行业包时 industry 写它的 id）；没通过就按它说的改，直到通过。通过的那一版会出现在管理员的表单里。
7. 描述里没说的（车间名、几条线、节拍）按行业常见的规模定，在最后的回答里说出你做的假设。

最后用两三句中文告诉管理员：建了什么（几个工厂、几条线、生产什么；新写了行业包就说它有哪些产品和设备），以及你做了哪些假设。不要贴 JSON。`

const industryPrompt = `你是 SmartFactory 的行业包编写员。管理员描述一类工厂（或场景）由什么组成，你写成一个行业包（industry）：产品长什么样、有哪些设备、工艺路线怎么走。

做法：
1. list_industries 看已有的行业包；get_industry 读一个结构最像的（离散制造看 forging 或 machining，流程/包装看 food，人或车走动的服务场景看 flow 为 walk 的），照它的结构写。
2. id 用英文小写和连字符；新写的不能和已有的重复，改一个现有的（管理员给了它的配置）就保持原 id。name 是中文行业名。
3. 产品 1～3 个，每个 2～4 个阶段（stages，每个阶段用几个 parts 画出样子）；设备 4～10 种，选合适的 look.archetype，cycle、power、thermal 按行业常识；每个产品一条 routes，3～8 步，makes 指向后面的阶段。
4. 有料号就写 partNo（或 variants），有客户、供应商就写 customers、suppliers；质检工位写 measure（目标值、上下限、sigma）。
   管理员讲到原料、配方、过敏原、设备分类、温度要求这类知识时，写进 ontology（本体，格式照 food 的）：classes 类和上级类，relations 关系，rules 规则，individuals 有名字的东西，bind 把产品、设备种类绑到类上，constraints 约束（读数限值或必须有的关系）。
5. propose_industry 检查；没通过就按它说的改，直到通过。通过的那一版交给管理员确认：通过一次就停，不要再改了重复提交。

最后用两三句中文说清楚：有哪些产品、哪些设备、几条路线，以及你按常识定的数（节拍、功率、公差）。不要贴 JSON。`

// ComposeKind is what the architect writes: a company (and the packs it needs), or a pack alone.
type ComposeKind string

const (
	ComposeCompany  ComposeKind = "company"
	ComposeIndustry ComposeKind = "industry"
)

// Compose writes a company config from brief (or, for ComposeIndustry, an industry pack),
// starting from base (the form's current config, may be empty). emit sees the steps, every
// industry pack that passed the check ("industry"), every company config that did ("draft") and
// the answer.
func (r *Runner) Compose(ctx context.Context, who auth.Who, kind ComposeKind, brief string, base json.RawMessage, guide string, emit func(Event)) error {
	if r.llm == nil {
		return ErrNoModel
	}
	prompt := architectPrompt
	if kind == ComposeIndustry {
		prompt = industryPrompt
	}
	svc, err := agentpkg.New("sf-architect").WithLLM(r.llm).WithSystemPrompt(prompt + "\n\n# 配置怎么写（参考）\n" + guide).
		WithDBPath(filepath.Join(r.dir, "agentgo.db")).WithTimezone(shanghai).Build()
	if err != nil {
		return err
	}
	defer svc.Close()
	eng := r.Eng
	var proposed json.RawMessage
	// packs written in this run, by id: a company may use them before they are installed
	packs := map[string]json.RawMessage{}
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
			id := a.str("id")
			if p, ok := packs[id]; ok {
				return raw(string(p))
			}
			js := eng.Industry(id)
			if js == "null" {
				return nil, fmt.Errorf("没有行业包 %q，先 list_industries", id)
			}
			return raw(js)
		}},
		{"propose_industry", "检查一个行业包（不会装上）。通过了交给管理员，建公司时一起装；没通过返回每个问题。", obj(map[string]any{
			"config": map[string]any{"type": "object", "description": "完整的行业包：id、name、unit、flow、products、machines、routes（以及可选的 inventory、terms、qualityLabel）"},
		}, "config"), func(a args) (any, error) {
			cfg, ok := a["config"].(map[string]any)
			if !ok {
				return nil, errors.New("config 要是一个对象")
			}
			b, _ := json.Marshal(cfg)
			id, _ := cfg["id"].(string)
			_, mine := packs[id]
			if !mine && id != baseID && strings.Contains(eng.IndustryList(), fmt.Sprintf(`"id":%q`, id)) {
				return nil, fmt.Errorf("已经有行业包叫 %q 了，换一个 id", id)
			}
			res := eng.CheckIndustry(b)
			if !res.OK {
				return nil, fmt.Errorf("没通过：\n- %s", strings.Join(res.Errors, "\n- "))
			}
			packs[id] = b
			if kind == ComposeIndustry {
				proposed = b
			}
			var pack map[string]any
			json.Unmarshal(b, &pack)
			emit(Event{Kind: "industry", Result: pack})
			return res.Detail, nil
		}},
	}
	if kind != ComposeIndustry {
		tools = append(tools,
			tl{"list_companies", "现有的公司：工厂、产线数和设备数。", obj(map[string]any{}), func(args) (any, error) { return raw(eng.List()) }},
			tl{"get_company", "一家现有公司的完整配置，照着写最稳。", idParam, func(a args) (any, error) {
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
			tl{"propose_company", "检查一份公司配置（不会创建）。通过了就放进管理员的表单；没通过返回每个问题。", obj(map[string]any{
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
				var res engine.ConfigResult
				if len(packs) > 0 {
					// with the packs written here, as a template would bring them
					t, _ := json.Marshal(map[string]any{"company": cfg, "industries": packs})
					res = eng.CheckTemplate(t)
				} else {
					res = eng.CheckCompany(b)
				}
				if !res.OK {
					return nil, fmt.Errorf("没通过：\n- %s", strings.Join(res.Errors, "\n- "))
				}
				proposed = b
				var draft map[string]any
				json.Unmarshal(b, &draft)
				emit(Event{Kind: "draft", Result: draft})
				return res.Detail, nil
			}})
	}
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.name
		run := t.run
		svc.AddToolWithMetadata(t.name, t.desc, t.params, func(_ context.Context, m map[string]any) (any, error) { return run(args(m)) },
			agentpkg.ToolMetadata{ReadOnly: true, ConcurrencySafe: !strings.HasPrefix(t.name, "propose_")})
	}

	goal := "管理员的描述：\n" + brief
	if len(base) > 0 {
		what := "管理员表单里现在是这份配置"
		if kind == ComposeIndustry {
			what = "要改的是这个行业包"
		}
		goal += "\n\n" + what + "，在它上面按描述改（id 不变，除非描述要求）：\n" + string(base)
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
			if strings.HasPrefix(ev.ToolName, "propose_") {
				args = nil // the config itself arrives as a draft or an industry
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
