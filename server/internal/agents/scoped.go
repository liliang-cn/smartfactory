package agents

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	agentpkg "github.com/liliang-cn/agent-go/v3/pkg/agent"

	"smartfactory/internal/auth"
	"smartfactory/internal/scoped"
)

// The part editor: an administrator picks one part of a built company (a plant, an area, a line,
// a machine) and says how it should change; the AI sees the whole config but writes only a short
// list of changes, which the server checks — the scope guard first — before anyone sees them.

const scopedPrompt = `你是 SmartFactory 的局部修改员。管理员在已经建好的工厂里选中了一处（一个工厂、车间、产线或一台设备），说这里要怎么改。你只改选中的这一处。

规则：
1. 下面给你整份配置看全貌，但只能改「可以改的范围」里的地方。范围外的改动（别的产线、别的车间、公司本身）会被整份拒绝，一处也不会采用。
2. 管理员要你改范围外的地方时（比如另一条产线），不要去改它；告诉管理员这里只能改选中的这一处，范围外的要回到工厂结构里选中那一处再改。范围里能做的部分照常改。
3. 用 propose_change 提交改动清单，不是整份配置。每项是 {op, path, value}：op 是 set（设一个值，或替换列表里的一项）、insert（插进列表，path 末尾写位置，写 - 是加到最后）、remove（删一个键或列表里的一项）。path 从配置的根写起，用 / 隔开，比如 plants/0/lines/1/steps/3/cycle。
4. 产线的工位在 steps 里，每项是对象：machine 是设备种类（下面设备种类表里的 key），name 名称，cycle 节拍（秒），thermal 是这台设备自己的温度限值 {warn 报警温度, trip 停机温度}（°C）。报警温度至少要比运行温度（设备种类表里的 run）高 6°C，停机温度要比报警温度高：只让你改报警温度时，停机温度不比它高的话，把停机温度也调到比报警温度高 10°C 左右，并在回答里说。
5. 位号（HP-201 这样）按「设备种类的编码-产线号+这种设备在这条线上的第几台」编，接入的设备、历史数据、点检记录都认位号。加工位尽量加在最后，这样已有的位号都不变；必须加在中间时，排在它后面的同种设备位号会往后移，在回答里说清楚。
6. 新工位的 machine 用设备种类表里这个工厂行业的 key；没有完全对得上的就选最接近的，在 name 里写清楚它做什么。
7. propose_change 通过了就停下，不要重复提交；没通过就按它说的改，最多再试两次。
最后用一两句中文告诉管理员改了什么、位号变没变。回答写纯文字：称呼用"你"，不用"您"；不用 ** 加粗、# 标题这类标记；不贴 JSON。`

// ScopedTask is the part to change and what the AI needs to know of the company.
type ScopedTask struct {
	Brief, Title, Path, Shared string
	// Config is the company's config, each line with its steps; Machines the packs' machine kinds.
	Config, Machines json.RawMessage
	// Tags lists the plant's lines and stations with their tags and places in the config.
	Tags string
}

// Scoped has the AI propose changes to one part of a company; check lays out and guards each
// proposal (and emits it when it passes). emit sees the steps and the answer.
func (r *Runner) Scoped(ctx context.Context, who auth.Who, t ScopedTask, check func([]scoped.Change) (any, error), emit func(Event)) error {
	if r.llm == nil {
		return ErrNoModel
	}
	svc, err := agentpkg.New("sf-scoped").WithLLM(r.llm).WithSystemPrompt(scopedPrompt).
		WithDBPath(filepath.Join(r.dir, "agentgo.db")).WithTimezone(shanghai).Build()
	if err != nil {
		return err
	}
	defer svc.Close()
	passed := false
	params := map[string]any{"type": "object", "required": []string{"changes"}, "properties": map[string]any{
		"changes": map[string]any{"type": "array", "description": "改动清单", "items": map[string]any{
			"type": "object", "required": []string{"op", "path"}, "properties": map[string]any{
				"op":    map[string]any{"type": "string", "enum": []string{"set", "insert", "remove"}},
				"path":  map[string]any{"type": "string", "description": "从配置的根写起，用 / 隔开：plants/0/lines/1/steps/3/cycle"},
				"value": map[string]any{"description": "set、insert 的值（数字、文字或对象）"},
			}}},
	}}
	svc.AddToolWithMetadata("propose_change", "检查一份改动清单（不会保存）：只在选中的范围里才通过；通过了交给管理员确认，返回改动的说明和位号的变化。", params,
		func(_ context.Context, m map[string]any) (any, error) {
			b, _ := json.Marshal(m["changes"])
			var changes []scoped.Change
			if err := json.Unmarshal(b, &changes); err != nil || len(changes) == 0 {
				return nil, errors.New("changes 要是一个非空的列表，每项 {op, path, value}")
			}
			v, err := check(changes)
			if err == nil {
				passed = true
			}
			return v, err
		}, agentpkg.ToolMetadata{ReadOnly: true})

	var goal strings.Builder
	goal.WriteString("管理员选中的是：" + t.Title + "\n可以改的范围：配置里 " + t.Path + " 下面")
	switch {
	case strings.Contains(t.Path, "/steps/"):
		goal.WriteString("（只是这一个工位，不能加减工位）")
	case strings.Contains(t.Path, "/lines/"):
		goal.WriteString("（这条产线的名称、产品、车间、速度和工位；不能删掉这条线）")
	case strings.Contains(t.Path, "/areas/") || strings.HasSuffix(t.Path, "/hall"):
		goal.WriteString("，以及这个车间里的产线；也可以把别的车间的线移进来（改那条线的 area）")
	default:
		goal.WriteString("（这个工厂的一切，工厂的 id 除外）")
	}
	if t.Shared != "" {
		goal.WriteString("\n注意：" + t.Shared)
	}
	goal.WriteString("\n\n管理员说：\n" + t.Brief)
	goal.WriteString("\n\n这个工厂的产线和位号：\n" + t.Tags)
	goal.WriteString("\n设备种类（按行业包，key → 名称、编码、默认节拍、运行温度 run、报警 warn、停机 trip）：\n" + string(t.Machines))
	goal.WriteString("\n\n整份配置：\n" + string(t.Config))

	ch, err := svc.RunStreamWithOptions(auth.With(ctx, who), goal.String(), agentpkg.WithSessionID(uuid.NewString()), agentpkg.WithMaxTurns(8))
	if err != nil {
		return err
	}
	var answer, partial, failure string
	for ev := range ch {
		if (ev.Type == agentpkg.EventTypeToolCall || ev.Type == agentpkg.EventTypeToolResult) && ev.ToolName != "propose_change" {
			continue
		}
		switch ev.Type {
		case agentpkg.EventTypeThinking:
			emit(Event{Kind: "thinking"})
		case agentpkg.EventTypeToolCall:
			emit(Event{Kind: "tool_call", Tool: ev.ToolName})
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
	answer = cmp.Or(answer, partial)
	switch {
	case ctx.Err() != nil:
	case !passed && strings.TrimSpace(answer) != "" && answer != failure:
		// it said why it changed nothing (out of bounds, or a question back): an answer
		emit(Event{Kind: "complete", Text: answer})
	case !passed:
		emit(Event{Kind: "error", Text: cmp.Or(strings.TrimSpace(cmp.Or(answer, failure)), "没有写出能通过检查的改动")})
	default:
		emit(Event{Kind: "complete", Text: answer})
	}
	return nil
}
