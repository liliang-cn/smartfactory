package agents

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"

	agentpkg "github.com/liliang-cn/agent-go/v3/pkg/agent"

	"smartfactory/internal/auth"
	"smartfactory/internal/shapes"
)

// The shape drawer: the administrator (or the industry-pack author) describes how a machine or
// a product looks, and a model builds it by writing a program in code mode (package shapes),
// revising it until the twin's checks pass. What comes back is parts, ready for look.parts or
// a product stage.

const shapePrompt = `你是 SmartFactory 的外形设计员。别人用中文说一台设备或一个产品长什么样，你写一段 JavaScript 程序，调用 build_shape 把它搭出来，放进数字孪生里。

做法：
1. 先想清楚它由哪几大块组成、各多大（米），再写程序。用变量和循环，不要一块块手抄数字。
2. 调 build_shape 运行程序。报告里有错误（哪一行、哪个零件）和尺寸，按它说的改，每次都把整段程序重新提交。
3. 检查通过、尺寸也对得上描述，就停下，用一两句中文说搭了什么（不要贴程序）。

要点：
- 设备要撑满给它的外框，看起来才是真实大小；传送带那一条通道不能挡（工作台、底座放在带子下面或旁边，滑块、刀头悬在上面）。
- 零件数有上限：一个工厂有几十台设备，每个零件都要画，用少量大块零件勾出轮廓就好，不要做螺丝这种细节。
- 颜色优先用调色板里的名字（blue、blueDeep、white、metal、greyDark、yellow……），和工厂里其他设备一个风格。
- 产品是一件工件在线上的样子：放在原点、底面贴着 y = 0，长度方向沿 x。`

// ShapeAttempt is one checked program, for the console's progress lines.
type ShapeAttempt struct {
	N   int  `json:"n"`
	Max int  `json:"max"`
	OK  bool `json:"ok"`
	// Errors counts what the checks found, by kind: overlap, envelope, belt, floor, count, size, empty.
	Errors map[string]int `json:"errors,omitempty"`
	// Program is set when the program itself failed (a typo, a wrong option), with its line.
	Program string `json:"program,omitempty"`
	Line    int    `json:"line,omitempty"`
}

// DrawnShape is what DrawShape hands back.
type DrawnShape struct {
	OK       bool          `json:"ok"`
	Attempts int           `json:"attempts"`
	Parts    []shapes.Part `json:"parts"`
	// Size is look.size for a machine, [l, d, h] for a product.
	Size   [3]float64 `json:"size"`
	Min    [3]float64 `json:"min"`
	Max    [3]float64 `json:"max"`
	Answer string     `json:"answer,omitempty"`
	// Report is the kept attempt's check report (what is still wrong when OK is false).
	Report string `json:"report,omitempty"`
}

// ShapeAttempts bounds how many programs one drawing may take.
const ShapeAttempts = 6

var (
	attemptRe = regexp.MustCompile(`attempt (\d+)/(\d+): (PASSED|FAILED)`)
	programRe = regexp.MustCompile(`program error: (?:line (\d+)(?::\d+)?: )?([^\n]*)`)
)

// shapeProgress turns every build_shape call into events: a call starts, then the attempt.
type shapeProgress struct {
	emit func(Event)
	mu   sync.Mutex
	cats map[string]int
}

func (p *shapeProgress) Name() string { return "sf-shape-progress" }

func (p *shapeProgress) checked(c map[string]int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cats = c
}

func (p *shapeProgress) BeforeTool(_ context.Context, call agentpkg.ToolCallInfo) (agentpkg.ToolVerdict, error) {
	if call.Name == "build_shape" {
		p.checked(nil)
		p.emit(Event{Kind: "tool_call", Tool: "build_shape"})
	}
	return agentpkg.ToolVerdict{}, nil
}

func (p *shapeProgress) AfterTool(_ context.Context, res agentpkg.ToolResultInfo) (any, bool, error) {
	if res.Name != "build_shape" {
		return nil, false, nil
	}
	report, _ := res.Result.(string)
	a := ShapeAttempt{}
	if m := attemptRe.FindStringSubmatch(report); m != nil {
		a.N, _ = strconv.Atoi(m[1])
		a.Max, _ = strconv.Atoi(m[2])
		a.OK = m[3] == "PASSED"
	}
	if m := programRe.FindStringSubmatch(report); m != nil {
		a.Line, _ = strconv.Atoi(m[1])
		a.Program = strings.TrimSpace(m[2])
	}
	p.mu.Lock()
	a.Errors = p.cats
	p.mu.Unlock()
	if a.N == 0 && a.Program == "" { // refused (no attempts left) or an empty call
		a.Program = cmp.Or(firstLine(report), "没有运行")
	}
	p.emit(Event{Kind: "attempt", Tool: "build_shape", Result: a})
	return nil, false, nil
}

func firstLine(s string) string {
	s, _, _ = strings.Cut(strings.TrimSpace(s), "\n")
	return s
}

// DrawShape has the model draw what brief describes inside spec. emit sees each attempt
// ("tool_call", then "attempt"); the drawing itself is returned.
func (r *Runner) DrawShape(ctx context.Context, who auth.Who, spec shapes.Spec, what, brief string, emit func(Event)) (*DrawnShape, error) {
	if r.llm == nil {
		return nil, ErrNoModel
	}
	if emit == nil {
		emit = func(Event) {}
	}
	prog := &shapeProgress{emit: emit}
	spec.OnCheck = prog.checked
	svc, err := agentpkg.New("sf-shape").WithLLM(r.llm).WithSystemPrompt(shapePrompt).
		WithDBPath(filepath.Join(r.dir, "agentgo.db")).WithTimezone(shanghai).
		WithConstraintExtraction(false).
		WithCodeMode(shapes.Mode(spec), agentpkg.CodeModeAttempts(ShapeAttempts)).
		WithExtensions(prog).Build()
	if err != nil {
		return nil, err
	}
	defer svc.Close()
	goal := fmt.Sprintf("要画的%s：%s\n描述：%s", map[shapes.Target]string{shapes.Machine: "设备", shapes.Product: "产品"}[spec.Target], what, brief)
	out, err := svc.RunCode(auth.With(ctx, who), goal, agentpkg.WithMaxTurns(ShapeAttempts+4))
	if out == nil {
		return nil, cmp.Or(err, ErrNoAnswer)
	}
	d := &DrawnShape{OK: out.OK, Attempts: out.Attempts, Answer: strings.TrimSpace(out.Answer), Report: out.Report}
	if len(out.Artifact) > 0 {
		var art shapes.Drawn
		if err := json.Unmarshal(out.Artifact, &art); err != nil {
			return nil, err
		}
		d.Parts, d.Size, d.Min, d.Max = art.Parts, art.Size, art.Min, art.Max
	}
	if len(d.Parts) == 0 {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%s", cmp.Or(d.Answer, "没有画出来，换个说法再试一次"))
	}
	return d, nil
}

// drawForPack is the pack authors' draw_shape: a machine in the envelope it gives (the belt at
// the usual height and width), or a product within the largest a line takes.
func (r *Runner) drawForPack(ctx context.Context, who auth.Who, a args) (any, error) {
	spec := shapes.Spec{Target: shapes.Product, Envelope: shapes.ProductMax}
	if a.str("target") != "product" {
		spec = shapes.Spec{Target: shapes.Machine, Envelope: shapes.DefaultSize["frame"], BeltY: 0.9, BeltW: 0.9, Clear: 0.6}
		if sz, ok := a["size"].([]any); ok && len(sz) == 3 {
			for i, v := range sz {
				f, _ := v.(float64)
				if f < 0.5 || f > 20 {
					return nil, fmt.Errorf("size 的每一项要在 0.5～20 米之间")
				}
				spec.Envelope[i] = f
			}
		}
		spec.Envelope[1] = max(spec.Envelope[1], spec.BeltW+1)
		spec.Envelope[2] = max(spec.Envelope[2], spec.BeltY+spec.Clear+0.6)
	}
	brief := a.str("description")
	if strings.TrimSpace(brief) == "" {
		return nil, fmt.Errorf("description 写它长什么样")
	}
	d, err := r.DrawShape(ctx, who, spec, a.str("name"), brief, nil)
	if err != nil {
		return nil, err
	}
	out := map[string]any{"ok": d.OK, "attempts": d.Attempts, "parts": d.Parts}
	if spec.Target == shapes.Machine {
		out["size"] = d.Size
		out["use"] = "look.archetype 写 frame，look.size 写 size，look.parts 写 parts"
	} else {
		out["use"] = "放进这个阶段的 parts"
	}
	if !d.OK {
		out["note"] = "还有没通过的检查，parts 仍然能用"
	}
	return out, nil
}
