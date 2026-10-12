package knowledge

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
)

func f(x float64) *float64 { return &x }

// What happened to the machines joins the graph: a check out of spec, a station's latest checks,
// hints on its readings, its calibration and the commands AI sent it, each linked to the station
// and found from a question that names the station and asks about that kind. A few are kept on a
// station and none older than 30 days; what did not change is not written again; what ages out
// leaves; a source that fails keeps what was written; a rebuild writes the same again.
func TestEvents(t *testing.T) {
	kb, ctx := base(t)
	now := time.Now()
	var checks []CheckInfo
	for i := range perMachine + 4 {
		checks = append(checks, CheckInfo{ID: fmt.Sprint("CK-", i), FormName: "班前点检", Kind: "点检", Plant: "p1", Station: "RF-101",
			Taken: now.Add(-time.Duration(i+1) * time.Hour), By: "张工", Values: []CheckValue{
				{Label: "油温", Num: f(75 + float64(i)), Unit: "℃", Bad: true, Why: "超上限 70"},
				{Label: "油位", Text: "正常"},
			}})
	}
	checks = append(checks, CheckInfo{ID: "CK-OLD", FormName: "班前点检", Kind: "点检", Plant: "p1", Station: "HP-101", Taken: now.Add(-40 * 24 * time.Hour), By: "李工",
		Values: []CheckValue{{Label: "油位", Text: "低", Bad: true, Why: "低"}}},
		CheckInfo{ID: "CK-GONE", FormName: "班前点检", Kind: "点检", Plant: "p1", Station: "XX-999", Taken: now, By: "李工"})
	ended := now.Add(-time.Hour)
	anomalies := []AnomalyInfo{
		{ID: 1, Plant: "p1", Station: "RF-101", Field: "温度", State: "运行", Kind: "level", Since: now.Add(-2 * time.Hour), Value: 85.2, Z: 4.1, Mean: 72, Sigma: 3},
		{ID: 2, Plant: "p1", Station: "HP-101", Field: "振动", State: "运行", Kind: "drift", Since: now.Add(-5 * time.Hour), Ended: &ended, Peak: -5, Mean: 4, Sigma: 0.2},
	}
	applied := now.Add(-3 * time.Hour)
	calibs := []CalibInfo{{Plant: "p1", Station: "RF-101", FittedAt: &applied, AppliedAt: &applied, AppliedBy: "王工",
		Params: []CalibParam{{Label: "运行温升", Unit: "℃", Template: 40, Fitted: f(46), Applied: f(46)}}, Replay: []CalibReplay{{Field: "温度", Before: 2.1, After: 0.8}}}}
	decided := now.Add(-30 * time.Minute)
	commands := []CommandInfo{
		{Key: "CQ-1", Request: "CQ-1", Plant: "p1", Station: "RF-101", Cmd: "stop", Who: "claude", Via: "mcp", At: now.Add(-time.Hour), Status: "denied",
			Reason: "这家公司的 AI 只能提出指令，由人确认", DecidedBy: "张工", DecidedAt: &decided, Result: "正在换模", Context: "状态 运行，温度 80°C"},
		{Key: "log-7", Plant: "p1", Station: "RF-101", Cmd: "speed 1.2", Who: "claude", Via: "mcp", At: now.Add(-2 * time.Hour), Status: "refused", Result: "温度太高（规则：温度高于报警线）"},
	}
	rounds := []RoundInfo{{FormName: "班前点检", Kind: "点检", Plant: "p1", Station: "RF-101", Status: "overdue", Since: &decided, Last: &checks[0].Taken, LastBy: "张工", LastBad: 1}}
	var failCalib error
	kb.Checks = func(string, time.Time) ([]CheckInfo, error) { return slices.Clone(checks), nil }
	kb.Rounds = func(string) ([]RoundInfo, error) { return slices.Clone(rounds), nil }
	kb.Anomalies = func(string, time.Time) ([]AnomalyInfo, error) { return slices.Clone(anomalies), nil }
	kb.Calibrations = func(string) ([]CalibInfo, error) { return slices.Clone(calibs), failCalib }
	kb.Commands = func(string, time.Time) ([]CommandInfo, error) { return slices.Clone(commands), nil }
	sync := func() int {
		t.Helper()
		before := kb.eventState("huaduan")
		if err := kb.SyncEvents(ctx, "huaduan"); err != nil {
			t.Fatal(err)
		}
		n := 0
		for k, e := range kb.eventState("huaduan").Items {
			if before.Items[k].Hash != e.Hash {
				n++
			}
		}
		return n
	}
	if n := sync(); n != perMachine+1+2+1+2 {
		t.Errorf("written: %d", n)
	}
	st, _ := kb.Stats(ctx, "huaduan")
	want := map[string]int{TCheck: perMachine + 1, TAnomaly: 2, TCalibration: 1, TCommand: 2}
	for k, n := range want {
		if st.Kinds[k] != n || st.Relations[eventRelation[k]] != n {
			t.Errorf("%s: %d nodes, %d relations, want %d", k, st.Kinds[k], st.Relations[eventRelation[k]], n)
		}
	}
	if st.Events != perMachine+6 || st.Docs != 0 {
		t.Errorf("passages: %+v", st)
	}

	// the station and what happened to it
	e, _ := kb.Lookup(ctx, "huaduan", "RF-101")
	got := map[string]int{}
	for _, l := range e.Links {
		if _, ok := eventRelation[l.Kind]; ok {
			got[l.Type]++
			if l.Title == "" {
				t.Errorf("no title: %+v", l)
			}
		}
	}
	if got[RCheckedOn] != perMachine+1 || got[RSeenOn] != 1 || got[RCalibrates] != 1 || got[RTargets] != 2 {
		t.Errorf("RF-101's links: %v", got)
	}
	if last := e.Links[len(e.Links)-1]; last.Kind != TCommand {
		t.Errorf("what happened comes after the process: %+v", e.Links)
	}
	// the passages do not declare the station: named by all of them it would be a hub the walk
	// does not start from
	if slices.ContainsFunc(e.Mentions, func(m string) bool { return strings.Contains(m, "点检") }) {
		t.Errorf("RF-101 is mentioned by what happened to it: %v", e.Mentions)
	}
	if a, _ := kb.Search(ctx, "huaduan", "RF-101 点检", nil, nil, 6); a.Mode != "ppr" {
		t.Errorf("not walked: %+v", a)
	}
	name := "RF-101 点检异常 " + clock(checks[0].Taken)
	c, _ := kb.Lookup(ctx, "huaduan", name)
	if c == nil || c.Kind != TCheck || len(c.Links) != 1 || c.Links[0].Name != "RF-101" || c.Links[0].Type != RCheckedOn {
		t.Fatalf("%s: %+v", name, c)
	}
	if !slices.Contains(c.Detail, Fact{"油温", "75 ℃，超上限 70", true}) || !slices.Contains(c.Detail, Fact{"填写", "张工 " + clock(checks[0].Taken), false}) {
		t.Errorf("a check's card: %+v", c.Detail)
	}
	if a, _ := kb.Lookup(ctx, "huaduan", "RF-101 温度偏高 "+clock(anomalies[0].Since)); a == nil || !slices.Contains(a.Detail, Fact{"平常", "72±3", false}) {
		t.Errorf("an anomaly's card: %+v", a)
	}
	if old, _ := kb.Lookup(ctx, "huaduan", "HP-101 点检异常 "+clock(checks[len(checks)-2].Taken)); old != nil {
		t.Error("a check 40 days old is kept")
	}

	// asked about
	first := func(q string) Passage {
		t.Helper()
		a, err := kb.Search(ctx, "huaduan", q, nil, nil, 6)
		if err != nil || len(a.Passages) == 0 {
			t.Fatalf("%s: %v %+v", q, err, a)
		}
		return a.Passages[0]
	}
	if p := first("RF-101 点检"); p.From != TCheck || !strings.Contains(p.Text, "油温") || !strings.Contains(p.Title, "RF-101") {
		t.Errorf("RF-101's checks: %+v", p)
	}
	if p := first("RF-101 最近点检有没有超标"); p.From != TCheck || !strings.Contains(p.Text, "75 ℃") {
		t.Errorf("RF-101's latest check: %+v", p)
	}
	if p := first("RF-101 读数异常"); p.From != TAnomaly || !strings.Contains(p.Text, "72±3") {
		t.Errorf("RF-101's hints: %+v", p)
	}
	if p := first("RF-101 校准过没有"); p.From != TCalibration || !strings.Contains(p.Text, "王工") {
		t.Errorf("RF-101's calibration: %+v", p)
	}
	if p := first("AI 对 RF-101 发过什么指令"); p.From != TCommand || !strings.Contains(p.Text, "stop") {
		t.Errorf("RF-101's commands: %+v", p)
	}
	// what happened does not crowd out the station's own passage
	if p := first("HP-101 的上游"); !strings.HasPrefix(p.Title, "HP-101") || p.From != "process" {
		t.Errorf("HP-101's upstream: %+v", p)
	}

	// nothing changed: nothing written
	if n := sync(); n != 0 {
		t.Errorf("written again: %d", n)
	}
	// a hint ends, a check ages out, a command is decided
	anomalies[0].Ended, anomalies[0].Peak = &decided, 5.5
	checks = checks[1:]
	commands = commands[1:]
	// the hint, the check now among the latest eight, the summary no longer naming the gone one's values
	if n := sync(); n != 1+1+1 {
		t.Errorf("written after a change: %d", n)
	}
	if p := first("RF-101 读数异常"); !strings.Contains(p.Text, "恢复正常") || !strings.Contains(p.Text, "+5.5σ") {
		t.Errorf("an ended hint: %+v", p)
	}
	if gone, _ := kb.Lookup(ctx, "huaduan", "RF-101 指令 stop "+clock(now.Add(-time.Hour))); gone != nil {
		t.Errorf("a command no longer kept: %+v", gone)
	}
	if st, _ := kb.Stats(ctx, "huaduan"); st.Kinds[TCommand] != 1 || st.Kinds[TCheck] != perMachine+1 || st.Events != perMachine+5 {
		t.Errorf("after: %+v", st)
	}
	// a source that fails keeps what was written
	failCalib, calibs = errors.New("down"), nil
	sync()
	if st, _ := kb.Stats(ctx, "huaduan"); st.Kinds[TCalibration] != 1 {
		t.Errorf("a failed source's: %+v", st.Kinds)
	}
	failCalib = nil
	sync()
	if st, _ := kb.Stats(ctx, "huaduan"); st.Kinds[TCalibration] != 0 {
		t.Errorf("a calibration no longer there: %+v", st.Kinds)
	}
	// a rebuild writes the same
	before, _ := kb.Stats(ctx, "huaduan")
	kb.ForgetEvents("huaduan")
	if n := sync(); n != before.Events {
		t.Errorf("rebuilt %d of %d", n, before.Events)
	}
	if st, _ := kb.Stats(ctx, "huaduan"); fmt.Sprint(st) != fmt.Sprint(before) {
		t.Errorf("a rebuild changed the graph:\n%+v\n%+v", before, st)
	}
}

func TestAskedKinds(t *testing.T) {
	got := askedKinds("RF-101 最近点检有没有超标、读数有没有异常、校准过没有、AI 对它发过什么指令")
	if len(got) != 4 {
		t.Errorf("%v", got)
	}
	if got := askedKinds("HP-101 的上游"); len(got) != 0 {
		t.Errorf("%v", got)
	}
}
