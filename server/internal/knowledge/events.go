package knowledge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/liliang-cn/cortexdb/v2/pkg/cortexdb"

	"smartfactory/internal/engine"
)

// What happened to a company's machines, in its knowledge base: checks filled in that found
// something out of spec, each station's latest checks, hints that a measured reading is unusual,
// calibrations, and the commands AI sent. Each is a node linked to its station, with a passage
// saying what happened, so "RF-101 最近点检有没有超标" walks from the station to its checks. Only
// the recent and notable are kept, a few on each station, and they are written when they change
// (a form filled in, a hint starting or ending, a calibration fitted or applied, a command
// decided) and every few minutes for what time alone changes (a round falling overdue, things
// ageing out) — never per reading, and nothing that has not changed.
//
// The passages name their station in the text but do not declare it: a station named by thirty
// passages would be a hub the walk no longer starts from (CortexDB drops an entity mentioned by
// more than a tenth of the passages as a seed). The relation to the station carries the walk, and
// Search weighs it up when the question asks about that kind (点检, 异常, 校准, 指令).

// CheckInfo is a form filled in on a machine that had something out of spec or abnormal.
type CheckInfo struct {
	ID, FormName string
	// Kind is the form's kind as people say it: 点检, 首件检验, 化验, 记录.
	Kind           string
	Plant, Station string
	Taken          time.Time
	By             string
	Values         []CheckValue
	Note           string
}

// CheckValue is one answer on a form.
type CheckValue struct {
	Label string
	// Text is an answer or a note; Num a number, in Unit.
	Text string
	Num  *float64
	Unit string
	// Bad: out of spec or abnormal; Why says how ("超上限 70").
	Bad   bool
	Why   string
	Photo bool
}

// RoundInfo is where a form stands on a machine: when it was last filled in and whether this
// round is overdue.
type RoundInfo struct {
	FormName, Kind string
	Plant, Station string
	// Status: done | due | overdue | open
	Status string
	// Since is when it fell overdue.
	Since   *time.Time
	Last    *time.Time
	LastBy  string
	LastBad int
}

// AnomalyInfo is a hint that a machine's reading is unusual for its state.
type AnomalyInfo struct {
	ID             int64
	Plant, Station string
	// Field and State as people say them: 温度, 运行.
	Field, State string
	// Kind: level | drift
	Kind  string
	Since time.Time
	Ended *time.Time
	// Value and Z when it began, Peak the furthest it went (once ended), Mean ± Sigma the normal.
	Value, Z, Peak, Mean, Sigma float64
}

// CalibInfo is a machine's calibration: its last fit, and whether that is in force.
type CalibInfo struct {
	Plant, Station string
	FittedAt       *time.Time
	From, To       *time.Time
	AppliedAt      *time.Time
	AppliedBy      string
	Params         []CalibParam
	Replay         []CalibReplay
}

type CalibParam struct {
	Label, Unit     string
	Template        float64
	Fitted, Applied *float64
}

// CalibReplay is how far the model was from the measurements with the template's parameters and
// with the fitted ones.
type CalibReplay struct {
	Field         string
	Before, After float64
}

// CommandInfo is a command an AI sent a machine: one that waited for a person, or one carried out
// or refused straight away.
type CommandInfo struct {
	// Key is unique among a company's commands: the request's id (CQ-…), or one of the log's.
	Key, Request   string
	Plant, Station string
	// Cmd is the command as the log writes it: "speed 1.2", "stop".
	Cmd      string
	Who, Via string
	At       time.Time
	// Status: waiting | sent | refused | denied | expired | failed
	Status string
	// Reason it waited, Context the machine as it was, Result the twin's answer or why not.
	Reason, Context, Result string
	DecidedBy               string
	DecidedAt               *time.Time
}

// Fact is one line of what happened, as a node's card shows it.
type Fact struct {
	Label string `json:"label"`
	Value string `json:"value"`
	// Bad: out of spec, abnormal, overdue, refused.
	Bad bool `json:"bad,omitempty"`
}

const (
	// keepEventDays is how long what happened is kept; perMachine how many of a kind on one
	// station, perKind how many of a kind in all.
	keepEventDays = 30
	perMachine    = 8
	perKind       = 300
	// eventFormat changes whenever eventItems writes differently, so everything is written again.
	eventFormat = "1"
)

// eventRelation is the relation from each kind of what happened to its station.
var eventRelation = map[string]string{TCheck: RCheckedOn, TAnomaly: RSeenOn, TCalibration: RCalibrates, TCommand: RTargets}

// askWords are how a question asks about each kind.
var askWords = map[string][]string{
	TCheck:       {"点检", "巡检", "检查", "检验", "首件", "化验", "超标", "超差", "不合格", "填写"},
	TAnomaly:     {"异常", "读数", "偏高", "偏低", "漂移", "不正常", "波动"},
	TCalibration: {"校准", "标定", "拟合", "校正"},
	TCommand:     {"指令", "命令", "下发", "批准", "审批", "远程"},
}

// askedKinds are the kinds of what happened a question asks about.
func askedKinds(q string) map[string]bool {
	out := map[string]bool{}
	for k, ws := range askWords {
		for _, w := range ws {
			if strings.Contains(q, w) {
				out[k] = true
			}
		}
	}
	if strings.Contains(q, "AI") {
		out[TCommand] = true
	}
	return out
}

// events are what the sources gave.
type events struct {
	checks    []CheckInfo
	rounds    []RoundInfo
	anomalies []AnomalyInfo
	calibs    []CalibInfo
	commands  []CommandInfo
}

type station struct{ name, desc string }

// valueText is an answer on a form as it reads: "油温 75 ℃，超上限 70".
func valueText(v CheckValue) string {
	what := v.Text
	switch {
	case v.Photo:
		what = "照片"
	case v.Num != nil:
		what = strings.TrimSpace(fnum(*v.Num) + " " + v.Unit)
	}
	if v.Bad && v.Why != "" && v.Why != what {
		what += "，" + v.Why
	}
	return v.Label + " " + what
}

func clock(t time.Time) string { return t.In(cst).Format("01-02 15:04") }

func fnum(x float64) string { return strconv.FormatFloat(x, 'f', -1, 64) }

func round3(x float64) string {
	return strings.TrimRight(strings.TrimRight(strconv.FormatFloat(x, 'f', 3, 64), "0"), ".")
}

// latest are the newest of list, perMachine on a station besides those always kept, perKind in all.
func latest[T any](list []T, at func(T) time.Time, on func(T) string, always func(T) bool, now time.Time) []T {
	list = slices.Clone(list)
	sort.SliceStable(list, func(i, j int) bool { return at(list[i]).After(at(list[j])) })
	n := map[string]int{}
	var out []T
	for _, x := range list {
		if len(out) >= perKind {
			break
		}
		keep := always != nil && always(x)
		if !keep && (now.Sub(at(x)) > keepEventDays*24*time.Hour || n[on(x)] >= perMachine) {
			continue
		}
		n[on(x)]++
		out = append(out, x)
	}
	return out
}

// eventItems are the passages of what happened, each with its node linked to its station. Things
// on a station the process no longer has are left out.
func eventItems(m *Model, ev events, now time.Time) []item {
	n := nameModel(m)
	stations := map[string]station{}
	for _, p := range m.Plants {
		for _, l := range p.Lines {
			for _, s := range l.Stations {
				stations[p.ID+"/"+s.ID] = station{n.station[p.ID+"/"+s.ID], s.Name}
			}
		}
	}
	used := map[string]int{}
	var out []item
	// add makes the node of kind called after its station and what it was ("RF-101 · 最近点检",
	// made unique), linked to the station, with its passage, one line about it (desc) and its card
	// (detail). The " · " keeps a question from naming it: CortexDB matches the words of a question
	// against the names in the graph, joining "RF-101" and "最近点检" with a space, and a node that
	// matched would take the station's place as where the walk starts.
	add := func(id, kind string, st station, what, desc, text string, at time.Time, detail []Fact) {
		name := st.name + " · " + what
		if used[name]++; used[name] > 1 {
			name += fmt.Sprintf(" #%d", used[name])
		}
		it := item{id: id, title: name, text: text, meta: map[string]string{"kind": kind, "station": st.name, "at": fmt.Sprint(at.UnixMilli())}}
		raw, _ := json.Marshal(detail)
		it.ents = append(it.ents, cortexdb.ToolEntityInput{Name: name, Type: kind, Description: desc, ChunkIDs: []string{chunkID(id)}, Metadata: map[string]string{"detail": string(raw)}})
		// the station is named by the relation only (see above)
		it.ents = append(it.ents, cortexdb.ToolEntityInput{Name: st.name, Type: TStation, Description: st.desc})
		it.rel(name, eventRelation[kind], st.name)
		out = append(out, it)
	}
	about := func(st station) string { return st.name + paren(st.desc) }

	// checks that found something, oldest first so a name made unique keeps its number
	checks := latest(ev.checks, func(c CheckInfo) time.Time { return c.Taken }, func(c CheckInfo) string { return c.Plant + "/" + c.Station }, nil, now)
	slices.Reverse(checks)
	// what was out of spec on each, for the station's latest checks
	badOf := map[string][]string{}
	for _, c := range checks {
		k := c.Plant + "/" + c.Station + "/" + c.FormName + "/" + fmt.Sprint(c.Taken.UnixMilli())
		for _, v := range c.Values {
			if v.Bad {
				badOf[k] = append(badOf[k], valueText(v))
			}
		}
	}
	for _, c := range checks {
		st, ok := stations[c.Plant+"/"+c.Station]
		if !ok {
			continue
		}
		var bad, rest []string
		detail := []Fact{{"表单", c.FormName, false}, {"填写", c.By + " " + clock(c.Taken), false}}
		for _, v := range c.Values {
			what := valueText(v)
			detail = append(detail, Fact{v.Label, strings.TrimPrefix(what, v.Label+" "), v.Bad})
			if v.Bad {
				bad = append(bad, what)
			} else if !v.Photo {
				rest = append(rest, what)
			}
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s %s 的「%s」（%s）由%s填写，%d 项不正常：%s。", about(st), clock(c.Taken), c.FormName, c.Kind, c.By, len(bad), strings.Join(bad, "；"))
		if len(rest) > 0 {
			fmt.Fprintf(&b, "其余正常：%s。", strings.Join(rest, "；"))
		}
		if c.Note != "" {
			fmt.Fprintf(&b, "备注：%s", c.Note)
			detail = append(detail, Fact{"备注", c.Note, false})
		}
		add("event:check:"+c.ID, TCheck, st, fmt.Sprintf("%s异常 %s", c.Kind, clock(c.Taken)),
			fmt.Sprintf("%s %s，%s", clock(c.Taken), c.By, strings.Join(bad, "；")), b.String(), c.Taken, detail)
	}

	// each station's latest checks, and the rounds it is behind on
	type forms struct {
		st     station
		key    string
		rounds []RoundInfo
	}
	var byStation []*forms
	index := map[string]*forms{}
	for _, r := range ev.rounds {
		k := r.Plant + "/" + r.Station
		st, ok := stations[k]
		if !ok || r.Last == nil && r.Status != "overdue" {
			continue
		}
		f := index[k]
		if f == nil {
			f = &forms{st: st, key: k}
			index[k] = f
			byStation = append(byStation, f)
		}
		f.rounds = append(f.rounds, r)
	}
	sort.Slice(byStation, func(i, j int) bool { return byStation[i].key < byStation[j].key })
	for _, f := range byStation {
		var parts []string
		var detail []Fact
		var last time.Time
		overdue := 0
		for _, r := range f.rounds {
			var what string
			if r.Last != nil {
				what = fmt.Sprintf("最近一次 %s %s填", clock(*r.Last), r.LastBy)
				if r.LastBad > 0 {
					what += fmt.Sprintf("，%d 项异常", r.LastBad)
					if bad := badOf[r.Plant+"/"+r.Station+"/"+r.FormName+"/"+fmt.Sprint(r.Last.UnixMilli())]; len(bad) > 0 {
						what += "（" + strings.Join(bad, "；") + "）"
					}
				} else {
					what += "，全部正常"
				}
				if r.Last.After(last) {
					last = *r.Last
				}
			} else {
				what = "还没填过"
			}
			if r.Status == "overdue" {
				overdue++
				what += "；这一轮超期没做"
				if r.Since != nil {
					what += fmt.Sprintf("（%s 起）", clock(*r.Since))
				}
			}
			parts = append(parts, fmt.Sprintf("「%s」（%s）%s", r.FormName, r.Kind, what))
			detail = append(detail, Fact{r.FormName, what, r.LastBad > 0 || r.Status == "overdue"})
		}
		desc := fmt.Sprintf("%d 张表", len(f.rounds))
		if overdue > 0 {
			desc += fmt.Sprintf("，%d 张超期", overdue)
		}
		at := last
		if at.IsZero() {
			at = now
		}
		add("event:checks:"+strings.ReplaceAll(f.key, "/", ":"), TCheck, f.st, "最近点检", desc,
			fmt.Sprintf("%s 的点检、检验记录：%s。", about(f.st), strings.Join(parts, "；")), at, detail)
	}

	// hints: those still on, and the latest that ended
	hints := latest(ev.anomalies, func(a AnomalyInfo) time.Time { return a.Since }, func(a AnomalyInfo) string { return a.Plant + "/" + a.Station },
		func(a AnomalyInfo) bool { return a.Ended == nil }, now)
	slices.Reverse(hints)
	for _, a := range hints {
		st, ok := stations[a.Plant+"/"+a.Station]
		if !ok {
			continue
		}
		dir := "偏高"
		if a.Peak < 0 || a.Peak == 0 && a.Z < 0 {
			dir = "偏低"
		}
		if a.Kind == "drift" {
			dir = "缓慢" + dir
		}
		normal := round3(a.Mean) + "±" + round3(a.Sigma)
		detail := []Fact{{"读数", a.Field, false}, {"状态", a.State, false}, {"开始", clock(a.Since), false}, {"平常", normal, false}}
		var b strings.Builder
		fmt.Fprintf(&b, "%s 的%s读数异常（%s时%s）：%s 起", about(st), a.Field, a.State, dir, clock(a.Since))
		desc := ""
		if a.Ended == nil {
			fmt.Fprintf(&b, "，开始时 %s（%+.1fσ），这台设备%s时平常 %s，到现在还没恢复。", round3(a.Value), a.Z, a.State, normal)
			detail = append(detail, Fact{"开始时", fmt.Sprintf("%s（%+.1fσ）", round3(a.Value), a.Z), true}, Fact{"结束", "还没恢复", true})
			desc = fmt.Sprintf("还没恢复，%+.1fσ，平常 %s", a.Z, normal)
		} else {
			fmt.Fprintf(&b, "，这台设备%s时平常 %s，最远 %+.1fσ，%s 恢复正常。", a.State, normal, a.Peak, clock(*a.Ended))
			detail = append(detail, Fact{"最远", fmt.Sprintf("%+.1fσ", a.Peak), false}, Fact{"结束", clock(*a.Ended), false})
			desc = fmt.Sprintf("已恢复，最远 %+.1fσ，平常 %s", a.Peak, normal)
		}
		b.WriteString("这是按这台设备自己的读数学出来的提示，不是报警。")
		add(fmt.Sprint("event:anomaly:", a.ID), TAnomaly, st, fmt.Sprintf("%s%s %s", a.Field, dir, clock(a.Since)), desc, b.String(), a.Since, detail)
	}

	// calibrations in force, and fits of the last days
	calibs := slices.Clone(ev.calibs)
	sort.Slice(calibs, func(i, j int) bool {
		return calibs[i].Plant+"/"+calibs[i].Station < calibs[j].Plant+"/"+calibs[j].Station
	})
	for _, c := range calibs {
		st, ok := stations[c.Plant+"/"+c.Station]
		if !ok || c.AppliedAt == nil && (c.FittedAt == nil || now.Sub(*c.FittedAt) > keepEventDays*24*time.Hour) {
			continue
		}
		var b strings.Builder
		var detail []Fact
		at := now
		fmt.Fprintf(&b, "%s 的模型校准：", about(st))
		if c.FittedAt != nil {
			at = *c.FittedAt
			fmt.Fprintf(&b, "%s 用", clock(*c.FittedAt))
			if c.From != nil && c.To != nil {
				fmt.Fprintf(&b, " %s 到 %s ", clock(*c.From), clock(*c.To))
			}
			b.WriteString("的实测读数拟合")
			detail = append(detail, Fact{"拟合", clock(*c.FittedAt), false})
			var rp []string
			for _, r := range c.Replay {
				rp = append(rp, fmt.Sprintf("%s %s → %s", r.Field, round3(r.Before), round3(r.After)))
			}
			if len(rp) > 0 {
				fmt.Fprintf(&b, "，回放误差（模板参数 → 拟合参数）%s", strings.Join(rp, "、"))
				detail = append(detail, Fact{"回放误差", strings.Join(rp, "，"), false})
			}
			b.WriteString("。")
		}
		var ps []string
		for _, p := range c.Params {
			v := p.Applied
			if c.AppliedAt == nil {
				v = p.Fitted
			}
			if v == nil {
				continue
			}
			ps = append(ps, strings.TrimSpace(fmt.Sprintf("%s %s → %s %s", p.Label, round3(p.Template), round3(*v), p.Unit)))
		}
		desc := "拟合了，没启用"
		if c.AppliedAt != nil {
			at = *c.AppliedAt
			fmt.Fprintf(&b, "%s 由%s启用，在用的参数（模板 → 校准）：%s。", clock(*c.AppliedAt), c.AppliedBy, strings.Join(ps, "；"))
			detail = append(detail, Fact{"启用", c.AppliedBy + " " + clock(*c.AppliedAt), false})
			desc = "在用，" + c.AppliedBy + " " + clock(*c.AppliedAt) + "启用"
		} else {
			fmt.Fprintf(&b, "拟合出的参数没有启用（或已撤回），在用的是模板参数。拟合值（模板 → 拟合）：%s。", strings.Join(ps, "；"))
			detail = append(detail, Fact{"启用", "没有，在用模板参数", false})
		}
		for _, p := range ps {
			label, value, _ := strings.Cut(p, " ")
			detail = append(detail, Fact{label, value, false})
		}
		add("event:calib:"+c.Plant+":"+c.Station, TCalibration, st, "校准", desc, b.String(), at, detail)
	}

	// commands from AI: those waiting, and the latest
	cmds := latest(ev.commands, func(c CommandInfo) time.Time { return c.At }, func(c CommandInfo) string { return c.Plant + "/" + c.Station },
		func(c CommandInfo) bool { return c.Status == "waiting" }, now)
	slices.Reverse(cmds)
	for _, c := range cmds {
		st, ok := stations[c.Plant+"/"+c.Station]
		if !ok {
			continue
		}
		via := map[string]string{"mcp": "MCP 客户端", "assistant": "助手"}[c.Via]
		if via == "" {
			via = c.Via
		}
		status := commandStatus[c.Status]
		if status == "" {
			status = c.Status
		}
		var b strings.Builder
		fmt.Fprintf(&b, "%s %s（%s，AI）对 %s 发出指令 %s", clock(c.At), c.Who, via, about(st), c.Cmd)
		detail := []Fact{{"指令", c.Cmd, false}, {"发起", fmt.Sprintf("%s（%s）%s", c.Who, via, clock(c.At)), false}}
		if c.Request != "" {
			fmt.Fprintf(&b, "，等人批准（%s：%s）", c.Request, c.Reason)
			detail = append(detail, Fact{"请求", c.Request, false})
		}
		bad := c.Status != "sent" && c.Status != "waiting"
		fmt.Fprintf(&b, "。结果：%s", status)
		switch {
		case c.DecidedBy != "" && c.DecidedAt != nil:
			fmt.Fprintf(&b, "（%s %s）", c.DecidedBy, clock(*c.DecidedAt))
			detail = append(detail, Fact{"结果", fmt.Sprintf("%s（%s %s）", status, c.DecidedBy, clock(*c.DecidedAt)), bad})
		case c.DecidedAt != nil:
			fmt.Fprintf(&b, "（%s）", clock(*c.DecidedAt))
			detail = append(detail, Fact{"结果", fmt.Sprintf("%s（%s）", status, clock(*c.DecidedAt)), bad})
		default:
			detail = append(detail, Fact{"结果", status, bad})
		}
		if c.Result != "" {
			fmt.Fprintf(&b, "，%s", c.Result)
			detail = append(detail, Fact{"说明", c.Result, bad})
		}
		b.WriteString("。")
		if c.Context != "" {
			fmt.Fprintf(&b, "当时设备：%s。", c.Context)
			detail = append(detail, Fact{"当时设备", c.Context, false})
		}
		add("event:command:"+c.Key, TCommand, st, fmt.Sprintf("指令 %s %s", c.Cmd, clock(c.At)), status+"："+c.Who, b.String(), c.At, detail)
	}
	return out
}

var commandStatus = map[string]string{
	"waiting": "等待批准", "deciding": "正在处理", "sent": "已执行", "refused": "被规则拒绝", "denied": "被人拒绝", "expired": "过期没人批准", "failed": "执行失败",
}

// eventState is what was last written of what happened to a company's machines.
type eventState struct {
	Items map[string]eventEntry `json:"items"`
}

type eventEntry struct {
	Hash string `json:"hash"`
	Kind string `json:"kind"`
	Name string `json:"name"`
}

func (b *Base) eventPath(id string) string { return filepath.Join(b.dir, id+".events.json") }

func (b *Base) eventState(id string) eventState {
	st := eventState{Items: map[string]eventEntry{}}
	if raw, err := os.ReadFile(b.eventPath(id)); err == nil {
		json.Unmarshal(raw, &st)
	}
	if st.Items == nil {
		st.Items = map[string]eventEntry{}
	}
	return st
}

// SyncEvents writes what recently happened to the company's machines into its knowledge base:
// the new and changed, and removes what aged out. A kind whose source fails keeps what was written.
func (b *Base) SyncEvents(ctx context.Context, id string) error {
	if b.Checks == nil && b.Rounds == nil && b.Anomalies == nil && b.Calibrations == nil && b.Commands == nil {
		return nil
	}
	m, err := b.model(id)
	if err != nil {
		return err
	}
	now := time.Now()
	since := now.Add(-keepEventDays * 24 * time.Hour)
	var ev events
	failed := map[string]bool{}
	note := func(kind string, err error) {
		if err != nil {
			failed[kind] = true
			b.log.Warn("knowledge: what happened to the machines", "company", id, "kind", kind, "err", err)
		}
	}
	if b.Checks != nil {
		ev.checks, err = b.Checks(id, since)
		note(TCheck, err)
	}
	if b.Rounds != nil {
		ev.rounds, err = b.Rounds(id)
		note(TCheck, err)
	}
	if b.Anomalies != nil {
		ev.anomalies, err = b.Anomalies(id, since)
		note(TAnomaly, err)
	}
	if b.Calibrations != nil {
		ev.calibs, err = b.Calibrations(id)
		note(TCalibration, err)
	}
	if b.Commands != nil {
		ev.commands, err = b.Commands(id, since)
		note(TCommand, err)
	}
	return b.writeEvents(ctx, id, eventItems(m, ev, now), failed)
}

func (b *Base) writeEvents(ctx context.Context, id string, items []item, failed map[string]bool) error {
	st := b.eventState(id)
	next := eventState{Items: map[string]eventEntry{}}
	for k, e := range st.Items {
		if failed[e.Kind] {
			next.Items[k] = e
		}
	}
	var write []item
	for _, it := range items {
		kind := it.meta["kind"]
		if failed[kind] {
			continue
		}
		raw, _ := json.Marshal([]any{it.title, it.text, it.ents, it.rels})
		sum := sha256.Sum256(append(raw, eventFormat...))
		h := hex.EncodeToString(sum[:])
		next.Items[it.id] = eventEntry{Hash: h, Kind: kind, Name: it.title}
		if st.Items[it.id].Hash != h {
			write = append(write, it)
		}
	}
	var gone []string
	for k := range st.Items {
		if _, ok := next.Items[k]; !ok {
			gone = append(gone, k)
		}
	}
	if len(write) == 0 && len(gone) == 0 {
		return nil
	}
	c, err := b.open(id)
	if err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, k := range gone {
		c.db.DeleteKnowledge(ctx, cortexdb.KnowledgeDeleteRequest{KnowledgeID: k})
	}
	for _, it := range write {
		if err := save(ctx, c.db, it); err != nil {
			return fmt.Errorf("%s: %w", it.id, err)
		}
	}
	keep := map[string]map[string]bool{}
	for kind := range eventRelation {
		keep[kind] = map[string]bool{}
	}
	for _, e := range next.Items {
		keep[e.Kind][e.Name] = true
	}
	prune(ctx, c.db, keep)
	out, _ := json.Marshal(next)
	return os.WriteFile(b.eventPath(id), out, 0o644)
}

// ForgetEvents makes the next SyncEvents write everything again.
func (b *Base) ForgetEvents(id string) {
	if engine.ValidID.MatchString(id) {
		os.Remove(b.eventPath(id))
	}
}

// EventsChanged asks for what happened to a company's machines to be written again soon; changes
// arriving together are written together.
func (b *Base) EventsChanged(company string) {
	b.omu.Lock()
	b.edirty[company] = true
	b.omu.Unlock()
	select {
	case b.ekick <- struct{}{}:
	default:
	}
}

// eventsEvery is how often everything is looked at again, for what time alone changes.
const eventsEvery = 5 * time.Minute

// RunEvents keeps what happened to every company's machines in its base: all of it once at the
// start, a few seconds after something changes, and every few minutes.
func (b *Base) RunEvents(ctx context.Context) {
	tick := time.NewTicker(eventsEvery)
	defer tick.Stop()
	all := func() {
		b.omu.Lock()
		for _, c := range b.eng.CompanyIDs() {
			b.edirty[c] = true
		}
		b.omu.Unlock()
	}
	all()
	for {
		// let a burst of changes (a hint ending on several fields, a batch of checks) settle
		select {
		case <-ctx.Done():
			return
		case <-time.After(5 * time.Second):
		}
		b.omu.Lock()
		dirty := b.edirty
		b.edirty = map[string]bool{}
		b.omu.Unlock()
		for c := range dirty {
			if err := b.SyncEvents(ctx, c); err != nil {
				b.log.Error("knowledge: what happened to the machines", "company", c, "err", err)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-b.ekick:
		case <-tick.C:
			all()
		}
	}
}
