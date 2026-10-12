package agents

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	agentpkg "github.com/liliang-cn/agent-go/v3/pkg/agent"
	"github.com/liliang-cn/agent-go/v3/pkg/domain"
	"github.com/liliang-cn/agent-go/v3/pkg/providers"

	"smartfactory/internal/audit"
	"smartfactory/internal/auth"
	"smartfactory/internal/checks"
	"smartfactory/internal/engine"
	"smartfactory/internal/erp"
	"smartfactory/internal/knowledge"
)

var shanghai = func() *time.Location {
	if l, err := time.LoadLocation("Asia/Shanghai"); err == nil {
		return l
	}
	return time.FixedZone("CST", 8*3600)
}()

func now() time.Time { return time.Now().In(shanghai) }
func today() string  { return now().Format("2006-01-02") }

var weekday = []string{"周日", "周一", "周二", "周三", "周四", "周五", "周六"}

// Runner holds the companies' assistants: the store, and one agent-go service per assistant,
// built when it is first asked something and rebuilt when its instructions change.
type Runner struct {
	Store *Store
	Eng   *engine.Engine
	Log   *slog.Logger
	// Audit keeps what the assistants write, under the person they wrote it for.
	Audit *audit.Log
	// KB is the companies' knowledge bases; without one the assistants search the library alone.
	KB *knowledge.Base
	// Orders are the production orders the companies' ERPs sent.
	Orders *erp.Store
	// Checks are the forms people fill in by hand at the machines, and what they wrote.
	Checks *checks.Service
	// Hints are the measured machines' readings unusual for them.
	Hints Hints

	dir string
	llm domain.Generator

	mu   sync.Mutex
	svcs map[string]*built
}

type built struct {
	svc     *agentpkg.Service
	updated time.Time
}

// New opens the store in dir. The model comes from LLM_BASE_URL, LLM_API_KEY and LLM_MODEL;
// without them the assistants can be set up and their data browsed, but not asked.
func New(dir string, eng *engine.Engine, log *slog.Logger) (*Runner, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	st, err := Open(dir)
	if err != nil {
		return nil, err
	}
	r := &Runner{Store: st, Eng: eng, Log: log, dir: dir, svcs: map[string]*built{}}
	// agent-go keeps its own state (conversation history) under AGENTGO_HOME
	if os.Getenv("AGENTGO_HOME") == "" {
		os.Setenv("AGENTGO_HOME", filepath.Join(dir, "agentgo"))
	}
	if key := os.Getenv("LLM_API_KEY"); key != "" {
		p, err := providers.NewOpenAILLMProvider(&domain.OpenAIProviderConfig{BaseURL: os.Getenv("LLM_BASE_URL"), APIKey: key, LLMModel: os.Getenv("LLM_MODEL")})
		if err != nil {
			st.Close()
			return nil, err
		}
		r.llm = p
	}
	return r, nil
}

// Ready reports whether there is a model to answer with.
func (r *Runner) Ready() bool { return r.llm != nil }

func (r *Runner) Close() error {
	r.mu.Lock()
	for _, b := range r.svcs {
		b.svc.Close()
	}
	r.svcs = map[string]*built{}
	r.mu.Unlock()
	return r.Store.Close()
}

// Forget drops an assistant's service (it was changed or deleted).
func (r *Runner) Forget(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if b, ok := r.svcs[id]; ok {
		b.svc.Close()
		delete(r.svcs, id)
	}
}

func (r *Runner) prompt(a Agent) string {
	t, _ := template(a.Kind)
	name := a.Company
	var list []struct{ ID, Name string }
	if r.Eng != nil && json.Unmarshal([]byte(r.Eng.List()), &list) == nil {
		for _, c := range list {
			if c.ID == a.Company {
				name = c.Name
			}
		}
	}
	p := fmt.Sprintf("你是%s的「%s」。\n\n%s\n%s", name, a.Name, t.prompt, commonPrompt)
	if s := strings.TrimSpace(a.Instructions); s != "" {
		p += "\n\n公司给你的要求（优先于上面的做法）：\n" + s
	}
	return p
}

func (r *Runner) service(a Agent) (*agentpkg.Service, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if b, ok := r.svcs[a.ID]; ok && b.updated.Equal(a.Updated) {
		return b.svc, nil
	} else if ok {
		b.svc.Close()
		delete(r.svcs, a.ID)
	}
	t, ok := template(a.Kind)
	if !ok {
		return nil, fmt.Errorf("unknown kind %q", a.Kind)
	}
	svc, err := agentpkg.New("sf-" + a.ID).WithLLM(r.llm).WithSystemPrompt(r.prompt(a)).
		WithDBPath(filepath.Join(r.dir, "agentgo.db")).WithTimezone(shanghai).Build()
	if err != nil {
		return nil, err
	}
	all := tools(r.Store, r.Eng, a.Company)
	if r.KB != nil {
		for _, t := range knowledgeTools(r.KB, a.Company) {
			all[t.name] = t
		}
	}
	if r.Orders != nil {
		t := ordersTool(r.Orders, a.Company)
		all[t.name] = t
	}
	if r.Checks != nil {
		t := checksTool(r.Checks, a.Company)
		all[t.name] = t
	}
	if r.Hints != nil {
		t := anomalyTool(r.Hints, a.Company)
		all[t.name] = t
	}
	writes := map[string]bool{}
	for _, name := range t.Tools {
		tl, ok := all[name]
		if !ok {
			continue // a part this server does not have (no ERP orders, no knowledge base)
		}
		writes[name] = tl.write
		svc.AddToolWithMetadata(tl.name, tl.desc, tl.params, func(ctx context.Context, m map[string]any) (any, error) {
			res, err := tl.run(ctx, args(m))
			if tl.write {
				r.Audit.Record(caller(ctx), "assistant", a.Company, tl.name, subject(res, args(m)), a.Name, err)
			}
			return res, err
		}, agentpkg.ToolMetadata{ReadOnly: !tl.write, ConcurrencySafe: !tl.write})
	}
	// Only the template's writes need a say; agent-go's own tools (task_complete …) never do.
	svc.SetPermissionPolicy(func(req agentpkg.PermissionRequest) bool { return writes[req.ToolName] })
	svc.SetPermissionHandler(permit(a.Company))
	r.svcs[a.ID] = &built{svc: svc, updated: a.Updated}
	return svc, nil
}

// permit decides whether the person a run acts for may write on company: members and above
// may. A refusal leaves the run going, so the model hands over what it would have saved.
func permit(company string) agentpkg.PermissionHandler {
	return func(ctx context.Context, req agentpkg.PermissionRequest) (*agentpkg.PermissionResponse, error) {
		w := caller(ctx)
		if w.Can(company, auth.Member) {
			return &agentpkg.PermissionResponse{Allowed: true, DecidedBy: w.Name}, nil
		}
		return &agentpkg.PermissionResponse{ContinueRun: true, DecidedBy: "smartfactory",
			Reason: fmt.Sprintf("只有这家公司的成员、操作员或管理员可以保存，提问的人是%s：没有保存，把整理好的内容直接给用户", w.RoleIn(company).Label())}, nil
	}
}

// subject names what a write touched, for the audit log.
func subject(res any, a args) string {
	switch v := res.(type) {
	case Note:
		return v.ID + " " + v.Title
	case *Note:
		return v.ID + " " + v.Title
	case Ticket:
		return v.ID + " " + v.Title
	case *Ticket:
		return v.ID + " " + v.Title
	}
	return a.str("id")
}

// Event is one step of an answer as it is written, for the browser.
type Event struct {
	Kind   string         `json:"kind"` // conversation | thinking | tool_call | tool_result | delta | reset | complete | error
	ID     string         `json:"id,omitempty"`
	Title  string         `json:"title,omitempty"`
	Tool   string         `json:"tool,omitempty"`
	Args   map[string]any `json:"args,omitempty"`
	Result any            `json:"result,omitempty"`
	Text   string         `json:"text,omitempty"`
}

// ErrNoModel is returned when the server has no model configured.
var ErrNoModel = errors.New("服务器没有配置大模型")

// Ask puts text to the assistant in conversation conv (whose earlier turns agent-go keeps), as
// who; emit sees every step. The answer and its steps are kept with the conversation.
func (r *Runner) Ask(ctx context.Context, a Agent, conv, text string, who auth.Who, emit func(Event)) error {
	if r.llm == nil {
		return ErrNoModel
	}
	t, ok := template(a.Kind)
	if !ok {
		return fmt.Errorf("unknown kind %q", a.Kind)
	}
	svc, err := r.service(a)
	if err != nil {
		return err
	}
	if _, err := r.Store.AddMessage(conv, Message{Role: "user", Text: text, Status: "done"}); err != nil {
		return err
	}
	mid, err := r.Store.AddMessage(conv, Message{Role: "assistant", Status: "running"})
	if err != nil {
		return err
	}
	n := now()
	asker := who.Name
	if role := who.RoleIn(a.Company); role != auth.Visitor {
		asker += "（" + role.Label() + "）"
	}
	goal := fmt.Sprintf("（现在是 %s %s %s，提问的是%s）\n\n%s", n.Format("2006-01-02"), weekday[n.Weekday()], n.Format("15:04"), asker, text)
	ch, err := svc.RunStreamWithOptions(auth.With(ctx, who), goal, agentpkg.WithSessionID(conv), agentpkg.WithMaxTurns(24))
	if err != nil {
		r.Store.FinishMessage(mid, "", nil, "failed")
		return err
	}
	var steps []Step
	var answer, partial, failure string
	for ev := range ch {
		// agent-go's own tools (task_complete …) are how a run ends, not steps of the answer
		if (ev.Type == agentpkg.EventTypeToolCall || ev.Type == agentpkg.EventTypeToolResult) && !slices.Contains(t.Tools, ev.ToolName) {
			continue
		}
		switch ev.Type {
		case agentpkg.EventTypeThinking:
			emit(Event{Kind: "thinking"})
		case agentpkg.EventTypeToolCall:
			steps = append(steps, Step{Tool: ev.ToolName, Args: ev.ToolArgs})
			emit(Event{Kind: "tool_call", Tool: ev.ToolName, Args: ev.ToolArgs})
		case agentpkg.EventTypeToolResult:
			res := compact(ev.ToolResult)
			for i := len(steps) - 1; i >= 0; i-- {
				if steps[i].Tool == ev.ToolName && steps[i].Result == nil {
					steps[i].Result = res
					break
				}
			}
			emit(Event{Kind: "tool_result", Tool: ev.ToolName, Result: res})
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
	status := "done"
	switch {
	case ctx.Err() != nil:
		status = "stopped"
	case answer == "" && failure != "":
		status = "failed"
		answer = failure
	}
	r.Store.FinishMessage(mid, answer, steps, status)
	if status == "failed" {
		emit(Event{Kind: "error", Text: answer})
	} else {
		emit(Event{Kind: "complete", Text: answer})
	}
	return nil
}

// compact keeps a tool result small enough to send to a browser and keep.
func compact(v any) any {
	if s, ok := v.(string); ok {
		var out any
		if json.Unmarshal([]byte(s), &out) == nil {
			v = out
		} else if len(s) > 4000 {
			return s[:4000] + "…"
		} else {
			return s
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	if len(b) <= 8000 {
		var out any
		if json.Unmarshal(b, &out) == nil {
			return out
		}
	}
	return string([]rune(string(b))[:min(len([]rune(string(b))), 3000)]) + "…"
}

// NewAgent builds an agent on the assistants' model for something else the server runs (the
// console agent); the caller adds its tools and closes it.
func (r *Runner) NewAgent(name, prompt string) (*agentpkg.Service, error) {
	if r.llm == nil {
		return nil, ErrNoModel
	}
	return agentpkg.New(name).WithLLM(r.llm).WithSystemPrompt(prompt).
		WithDBPath(filepath.Join(r.dir, "agentgo.db")).WithTimezone(shanghai).Build()
}

// Run puts goal to svc in session and passes its steps on as events: the calls of the tools
// named own (agent-go's own tools are how a run ends, not steps), the answer as it is written,
// and how it ended. It returns the answer.
func Run(ctx context.Context, svc *agentpkg.Service, goal, session string, turns int, own []string, emit func(Event)) (string, error) {
	ch, err := svc.RunStreamWithOptions(ctx, goal, agentpkg.WithSessionID(session), agentpkg.WithMaxTurns(turns))
	if err != nil {
		return "", err
	}
	var answer, partial, failure string
	for ev := range ch {
		if (ev.Type == agentpkg.EventTypeToolCall || ev.Type == agentpkg.EventTypeToolResult) && !slices.Contains(own, ev.ToolName) {
			continue
		}
		switch ev.Type {
		case agentpkg.EventTypeThinking:
			emit(Event{Kind: "thinking"})
		case agentpkg.EventTypeToolCall:
			emit(Event{Kind: "tool_call", Tool: ev.ToolName, Args: ev.ToolArgs})
		case agentpkg.EventTypeToolResult:
			emit(Event{Kind: "tool_result", Tool: ev.ToolName, Result: compact(ev.ToolResult)})
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
	case answer == "" && failure != "":
		emit(Event{Kind: "error", Text: failure})
		return "", errors.New(failure)
	default:
		emit(Event{Kind: "complete", Text: answer})
	}
	return answer, nil
}
