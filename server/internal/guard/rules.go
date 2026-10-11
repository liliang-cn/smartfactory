package guard

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"smartfactory/internal/engine"
)

// Level is how far a company lets AI (MCP clients, assistants) act on its machines.
type Level string

const (
	// Advisory: AI proposes, a person confirms every command that would reach a machine.
	Advisory Level = "advisory"
	// Guarded: AI commands pass the rules, and the kinds in Settings.Approve wait for a person.
	Guarded Level = "guarded"
	// Autonomous: AI commands pass the rules and go.
	Autonomous Level = "autonomous"
)

// Rule refuses a command when its conditions hold for the machine as the twin sees it now.
//
// When is one or more conditions joined by "and": a field, an operator (= != > >= < <=) and a
// number, a word or another field. Fields: state (running, idle, blocked, fault, stopped,
// maint, …), temp, warn, trip (°C), speed, health, alarms (how many are active), arg (the
// command's argument), maint and changing (1 when under maintenance, changing a die).
//
//	{"cmd": "start", "when": "temp > warn", "say": "温度 {temp}°C 超过报警线 {warn}°C"}
type Rule struct {
	// Cmd is the command it applies to, or "*" for every one.
	Cmd  string `json:"cmd"`
	When string `json:"when"`
	// Say is why the command is refused; {temp}, {warn}, {trip}, {speed}, {arg}, {state} and
	// {alarms} are filled in.
	Say string `json:"say"`
}

// Settings are a company's guard.
type Settings struct {
	Level Level `json:"level"`
	// Approve are the commands AI sends that wait for a person at the guarded level.
	Approve []string `json:"approve"`
	Rules   []Rule   `json:"rules"`
	// Expire is how many minutes a command waits for a person before it lapses.
	Expire int `json:"expire"`
}

// Commands are the commands a machine takes.
var Commands = []string{"start", "stop", "speed", "reset", "maint", "fault", "restock", "ship", "retool", "toollimit"}

// Defaults are a company's guard until its administrators change it: AI only proposes, and
// the rules a careful operator would keep anyway.
func Defaults() Settings {
	return Settings{
		Level:   Advisory,
		Approve: []string{"start", "speed", "reset", "fault", "restock", "ship", "retool", "toollimit"},
		Rules: []Rule{
			{Cmd: "start", When: "state = fault", Say: "设备故障中：先排除故障、复位，再启动"},
			{Cmd: "start", When: "temp > warn", Say: "温度 {temp}°C 超过报警线 {warn}°C，降温后再启动"},
			{Cmd: "reset", When: "temp >= trip", Say: "温度 {temp}°C 还没降到停机线 {trip}°C 以下"},
			{Cmd: "speed", When: "arg > 1.2", Say: "速度最多 120%，要的是 {arg}"},
			{Cmd: "speed", When: "arg < 0.5", Say: "速度最低 50%，要的是 {arg}"},
			{Cmd: "retool", When: "state = running", Say: "设备正在加工，停机后再换模"},
		},
		Expire: 15,
	}
}

var fields = []string{"state", "temp", "warn", "trip", "speed", "health", "alarms", "arg", "maint", "changing"}

var condRe = regexp.MustCompile(`^\s*([a-z]+)\s*(=|!=|>=|<=|>|<)\s*(\S+)\s*$`)

type cond struct{ field, op, value string }

// parse reads a rule's conditions, or says what is wrong with them.
func parse(when string) ([]cond, error) {
	var out []cond
	for _, part := range regexp.MustCompile(`\s+(?:and|且)\s+`).Split(strings.TrimSpace(when), -1) {
		m := condRe.FindStringSubmatch(part)
		if m == nil {
			return nil, fmt.Errorf("「%s」看不懂：写成 字段 比较 值，比如 temp > warn", part)
		}
		if !slices.Contains(fields, m[1]) {
			return nil, fmt.Errorf("没有字段 %s，能用的是 %s", m[1], strings.Join(fields, "、"))
		}
		out = append(out, cond{m[1], m[2], m[3]})
	}
	return out, nil
}

// Check says what is wrong with settings, nothing when they can be saved.
func (s Settings) Check() []string {
	var out []string
	if !slices.Contains([]Level{Advisory, Guarded, Autonomous}, s.Level) {
		out = append(out, "等级是 advisory、guarded 或 autonomous")
	}
	for _, c := range s.Approve {
		if !slices.Contains(Commands, c) {
			out = append(out, fmt.Sprintf("没有指令 %s，能用的是 %s", c, strings.Join(Commands, "、")))
		}
	}
	for i, r := range s.Rules {
		if r.Cmd != "*" && !slices.Contains(Commands, r.Cmd) {
			out = append(out, fmt.Sprintf("第 %d 条规则：没有指令 %s", i+1, r.Cmd))
		}
		if _, err := parse(r.When); err != nil {
			out = append(out, fmt.Sprintf("第 %d 条规则：%s", i+1, err))
		}
		if strings.TrimSpace(r.Say) == "" {
			out = append(out, fmt.Sprintf("第 %d 条规则：要写拒绝的理由", i+1))
		}
	}
	if s.Expire < 1 || s.Expire > 24*60 {
		out = append(out, "等待批准的时间是 1～1440 分钟")
	}
	return out
}

// values are the fields of a machine's state, and of the command's argument when it has one.
func values(a engine.AssetState, arg *float64) map[string]any {
	b := func(x bool) float64 {
		if x {
			return 1
		}
		return 0
	}
	v := map[string]any{
		"state": a.State, "temp": a.Temp, "warn": a.Warn, "trip": a.Trip, "speed": a.Speed, "health": a.Health,
		"alarms": float64(len(a.Alarms)), "maint": b(a.Maint), "changing": b(a.Changing),
	}
	if arg != nil {
		v["arg"] = *arg
	}
	return v
}

// holds reports whether every condition is true of v. A condition on something v lacks (an
// argument the command does not have, a limit the machine does not have) does not hold.
func holds(cs []cond, v map[string]any) bool {
	for _, c := range cs {
		left, ok := v[c.field]
		if !ok {
			return false
		}
		right := any(c.value)
		if x, ok := v[c.value]; ok {
			right = x
		} else if f, err := strconv.ParseFloat(c.value, 64); err == nil {
			right = f
		}
		if !compare(left, c.op, right) {
			return false
		}
	}
	return true
}

func compare(l any, op string, r any) bool {
	lf, lok := l.(float64)
	rf, rok := r.(float64)
	if lok && rok {
		if (op != "=" && op != "!=") && (math.IsNaN(lf) || math.IsNaN(rf)) {
			return false
		}
		switch op {
		case "=":
			return lf == rf
		case "!=":
			return lf != rf
		case ">":
			return lf > rf
		case ">=":
			return lf >= rf
		case "<":
			return lf < rf
		case "<=":
			return lf <= rf
		}
		return false
	}
	ls, rs := fmt.Sprint(l), fmt.Sprint(r)
	switch op {
	case "=":
		return ls == rs
	case "!=":
		return ls != rs
	}
	return false
}

// refusal is the first rule that refuses cmd on a machine in state a, with its reason filled
// in; nil when none does.
func (s Settings) refusal(cmd string, arg *float64, a engine.AssetState) (*Rule, string) {
	v := values(a, arg)
	for i := range s.Rules {
		r := s.Rules[i]
		if r.Cmd != "*" && r.Cmd != cmd {
			continue
		}
		cs, err := parse(r.When)
		if err != nil || (r.usesLimits() && a.Warn == 0) || !holds(cs, v) {
			continue
		}
		return &r, fill(r.Say, v)
	}
	return nil, ""
}

// usesLimits says whether the rule compares with a machine's temperature limits: it does not
// hold for a machine that has none.
func (r Rule) usesLimits() bool {
	return strings.Contains(r.When, "warn") || strings.Contains(r.When, "trip")
}

func fill(say string, v map[string]any) string {
	return regexp.MustCompile(`\{([a-z]+)\}`).ReplaceAllStringFunc(say, func(m string) string {
		x, ok := v[m[1:len(m)-1]]
		if !ok {
			return m
		}
		if f, ok := x.(float64); ok {
			return strconv.FormatFloat(f, 'f', -1, 64)
		}
		return fmt.Sprint(x)
	})
}

// describe is a machine's state as the audit log keeps it with a command.
func describe(a engine.AssetState) string {
	parts := []string{"状态 " + stateLabel(a.State)}
	if a.Fault != "" {
		parts = append(parts, "故障 "+a.Fault)
	}
	if a.Temp != 0 || a.Warn != 0 {
		t := fmt.Sprintf("温度 %g°C", a.Temp)
		if a.Warn > 0 {
			t += fmt.Sprintf("（报警线 %g，停机线 %g）", a.Warn, a.Trip)
		}
		parts = append(parts, t)
	}
	if a.Speed > 0 {
		parts = append(parts, fmt.Sprintf("速度 %g%%", math.Round(a.Speed*100)))
	}
	if len(a.Alarms) > 0 {
		parts = append(parts, "报警 "+strings.Join(a.Alarms, "；"))
	}
	if a.Live != "" {
		parts = append(parts, "数据来自 "+a.Live)
	}
	return strings.Join(parts, "，")
}

var stateLabels = map[string]string{
	"running": "运行", "idle": "待料", "blocked": "阻塞", "fault": "故障", "stopped": "停机", "maint": "维护", "starved": "待料", "changeover": "换模",
}

func stateLabel(s string) string {
	if l, ok := stateLabels[s]; ok {
		return l
	}
	return s
}
