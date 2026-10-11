package guard

import (
	"strings"
	"testing"

	"smartfactory/internal/engine"
)

// The default rules against machines in different states, and what a rule may say.
func TestRules(t *testing.T) {
	d := Defaults()
	press := engine.AssetState{State: "running", Temp: 60, Warn: 72, Trip: 84, Speed: 1}
	f := func(x float64) *float64 { return &x }
	cases := []struct {
		name  string
		cmd   string
		arg   *float64
		state func(engine.AssetState) engine.AssetState
		say   string // "" when allowed
	}{
		{"start a running press", "start", nil, nil, ""},
		{"start a faulty press", "start", nil, func(a engine.AssetState) engine.AssetState { a.State = "fault"; return a }, "故障"},
		{"start a hot press", "start", nil, func(a engine.AssetState) engine.AssetState { a.Temp = 75; return a }, "75°C 超过报警线 72°C"},
		{"start a machine without limits", "start", nil, func(a engine.AssetState) engine.AssetState { a.Warn, a.Trip, a.Temp = 0, 0, 30; return a }, ""},
		{"reset above the trip line", "reset", nil, func(a engine.AssetState) engine.AssetState { a.Temp = 84; return a }, "停机线 84°C"},
		{"speed within limits", "speed", f(1.1), nil, ""},
		{"speed too high", "speed", f(1.3), nil, "120%，要的是 1.3"},
		{"speed too low", "speed", f(0.4), nil, "最低 50%"},
		{"retool while running", "retool", nil, nil, "停机后再换模"},
		{"retool while stopped", "retool", nil, func(a engine.AssetState) engine.AssetState { a.State = "stopped"; return a }, ""},
		{"stop always", "stop", nil, func(a engine.AssetState) engine.AssetState { a.State = "fault"; a.Temp = 90; return a }, ""},
	}
	for _, c := range cases {
		a := press
		if c.state != nil {
			a = c.state(a)
		}
		_, say := d.refusal(c.cmd, c.arg, a)
		if (c.say == "") != (say == "") || !strings.Contains(say, c.say) {
			t.Errorf("%s: %q, want %q", c.name, say, c.say)
		}
	}

	if p := d.Check(); len(p) > 0 {
		t.Errorf("the defaults do not check: %v", p)
	}
	for when, ok := range map[string]bool{
		"temp > warn":                    true,
		"state = running and alarms > 0": true,
		"state = running 且 speed >= 1.1": true,
		"pressure > 3":                   false,
		"temp >> 3":                      false,
		"temp":                           false,
	} {
		s := Defaults()
		s.Rules = []Rule{{Cmd: "start", When: when, Say: "x"}}
		if got := len(s.Check()) == 0; got != ok {
			t.Errorf("%q checks %v, want %v", when, got, ok)
		}
	}
	s := Defaults()
	s.Rules = []Rule{{Cmd: "*", When: "alarms > 0 and state != stopped", Say: "有 {alarms} 个报警"}}
	if _, say := s.refusal("maint", nil, engine.AssetState{State: "running", Alarms: []string{"温度偏高", "振动大"}}); say != "有 2 个报警" {
		t.Errorf("a rule for every command: %q", say)
	}
	s.Level, s.Expire = "careful", 0
	if p := s.Check(); len(p) != 2 {
		t.Errorf("a level and an expiry that do not exist: %v", p)
	}
}
