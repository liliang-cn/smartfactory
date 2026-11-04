package agents

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/liliang-cn/agent-go/v3/pkg/domain"

	"smartfactory/internal/auth"
	"smartfactory/internal/shapes"
)

// The first program puts the bolster on the belt and the flywheel through the back of the
// frame; the second moves them.
const pressTry1 = `const back = -frame.D / 2 + 0.25;
shape.box({w: 1.6, h: 0.6, d: 1.4}, {y: 0.3, z: -0.1}, {name: 'bed', color: 'blueDeep'});
shape.box({w: 1.2, h: 0.3, d: 0.8}, {y: frame.beltY + 0.1}, {name: 'bolster', color: 'metal'});
shape.box({w: 1.4, h: 3.0, d: 0.4}, {y: 0.6 + 1.5, z: back}, {name: 'frame', color: 'blue'});
shape.cylinder({r: 0.1, h: 0.2}, {x: 0, y: 2.0, z: back}, {name: 'hub', rotation: {x: 90}, color: 'metal'});
shape.box({w: 1.4, h: 0.6, d: 1.0}, {y: 3.9, z: -0.15}, {name: 'crown', color: 'blue'});`

const pressTry2 = `const back = -frame.D / 2 + 0.25;
shape.box({w: 1.6, h: 0.6, d: 1.4}, {y: 0.3, z: -0.1}, {name: 'bed', color: 'blueDeep'});
shape.box({w: 1.4, h: 3.0, d: 0.4}, {y: 0.6 + 1.5, z: back}, {name: 'frame', color: 'blue'});
shape.box({w: 1.4, h: 0.6, d: 1.0}, {y: 3.9, z: -0.15}, {name: 'crown', color: 'blue'});
shape.box({w: 0.6, h: 1.9, d: 0.6}, {y: 2.65}, {name: 'ram', color: 'metal', anim: {type: 'stroke', axis: 'y', to: -0.3}});
shape.cylinder({r: 0.4, h: 0.18}, {x: -0.78, y: 3.2, z: -0.45}, {name: 'flywheel', rotation: {z: 90}, color: 'greyDark', anim: {type: 'spin', axis: 'y', speed: 5}});`

// shapeLLM plays a model that draws a press wrong, reads the report, and fixes it.
type shapeLLM struct {
	mu      sync.Mutex
	reports []string
}

func (l *shapeLLM) answer(messages []domain.Message) *domain.GenerationResult {
	l.mu.Lock()
	defer l.mu.Unlock()
	var results []string
	for _, m := range messages {
		if m.Role == "tool" {
			results = append(results, m.Content)
		}
	}
	l.reports = results
	call := func(code string) *domain.GenerationResult {
		return &domain.GenerationResult{FinishReason: "tool_calls", ToolCalls: []domain.ToolCall{{
			ID: "c" + string(rune('a'+len(results))), Type: "function",
			Function: domain.FunctionCall{Name: "build_shape", Arguments: map[string]any{"code": code}},
		}}}
	}
	switch len(results) {
	case 0:
		return call(pressTry1)
	case 1:
		return call(pressTry2)
	}
	return &domain.GenerationResult{Content: "画了一台开式冲床：床身在传送带下面，滑块悬在上方，左边一个飞轮。", FinishReason: "stop"}
}

func (l *shapeLLM) Generate(context.Context, string, *domain.GenerationOptions) (string, error) {
	return "", nil
}
func (l *shapeLLM) Stream(context.Context, string, *domain.GenerationOptions, func(string)) error {
	return nil
}
func (l *shapeLLM) GenerateWithTools(_ context.Context, m []domain.Message, _ []domain.ToolDefinition, _ *domain.GenerationOptions) (*domain.GenerationResult, error) {
	return l.answer(m), nil
}
func (l *shapeLLM) StreamWithTools(_ context.Context, m []domain.Message, _ []domain.ToolDefinition, _ *domain.GenerationOptions, cb domain.ToolCallCallback) error {
	return cb(l.answer(m))
}
func (l *shapeLLM) GenerateStructured(context.Context, string, any, *domain.GenerationOptions) (*domain.StructuredResult, error) {
	return &domain.StructuredResult{Valid: true, Raw: "{}"}, nil
}
func (l *shapeLLM) RecognizeIntent(context.Context, string) (*domain.IntentResult, error) {
	return nil, nil
}

func TestDrawShapeFixesOnSecondAttempt(t *testing.T) {
	t.Setenv("LLM_API_KEY", "")
	t.Setenv("AGENTGO_HOME", t.TempDir())
	r, err := New(t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	llm := &shapeLLM{}
	r.llm = llm

	spec := shapes.Spec{Target: shapes.Machine, Envelope: [3]float64{2.4, 1.8, 4.4}, BeltY: 0.9, BeltW: 0.9, Clear: 0.6}
	var events []Event
	who := auth.Who{Key: "k", Name: "管理员", Role: auth.Admin}
	d, err := r.DrawShape(context.Background(), who, spec, "冲床", "带顶冠的开式冲床，左边有飞轮", func(e Event) { events = append(events, e) })
	if err != nil {
		t.Fatal(err)
	}
	if !d.OK || d.Attempts != 2 || len(d.Parts) != 5 || d.Size != spec.Envelope {
		t.Fatalf("ok %v attempts %d parts %d size %v\n%s", d.OK, d.Attempts, len(d.Parts), d.Size, d.Report)
	}
	if !strings.Contains(llm.reports[0], `"bolster": blocks the belt corridor`) || !strings.Contains(llm.reports[0], `"hub": is buried inside "frame"`) {
		t.Fatalf("first report:\n%s", llm.reports[0])
	}
	// progress: call, attempt 1 failed (one belt, one overlap), call, attempt 2 passed
	var attempts []ShapeAttempt
	calls := 0
	for _, e := range events {
		switch e.Kind {
		case "tool_call":
			calls++
		case "attempt":
			attempts = append(attempts, e.Result.(ShapeAttempt))
		}
	}
	if calls != 2 || len(attempts) != 2 {
		t.Fatalf("events %+v", events)
	}
	a1, a2 := attempts[0], attempts[1]
	if a1.N != 1 || a1.Max != ShapeAttempts || a1.OK || a1.Errors["belt"] != 1 || a1.Errors["overlap"] != 1 {
		t.Fatalf("attempt 1 %+v", a1)
	}
	if a2.N != 2 || !a2.OK || len(a2.Errors) != 0 {
		t.Fatalf("attempt 2 %+v", a2)
	}
	fly := d.Parts[4]
	if fly.Shape != "cylinder" || fly.Rotate != [3]float64{0, 0, 90} || fly.At != [3]float64{-0.78, 2.8, -0.45} {
		t.Fatalf("flywheel %+v", fly)
	}
	if strings.Contains(d.Answer, "shape.") {
		t.Fatalf("answer leaks code: %q", d.Answer)
	}
}
