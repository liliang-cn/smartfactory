package ops

import (
	"bytes"
	"cmp"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"
	"time"

	semantic "github.com/liliang-cn/semantic-go"

	"smartfactory/internal/audit"
	"smartfactory/internal/auth"
	"smartfactory/internal/calib"
	"smartfactory/internal/erp"
	"smartfactory/internal/guard"
	"smartfactory/internal/history"
)

// The value report: what the twin and its AI did for a company over a period — how often people
// took the advisor's plans and what adopting them changed, what the AI proposed or sent to
// machines and what became of it, calibration, orders on time, and how the plant did week by
// week. Every figure says what it was counted from and how many records that was; where there
// is nothing to count it from, its value is absent and the page says 没有数据 — nothing is
// estimated to fill a gap.

// ErrNoAdvisor is a company without an advisor service: it has no plans to count.
var ErrNoAdvisor = errors.New("这家公司没有接顾问服务")

// PlantHistory is a company's history database, read by day.
type PlantHistory interface {
	Days(ctx context.Context, from, to time.Time) ([]history.Day, error)
}

// Period is the span a report covers: From inclusive, To exclusive.
type Period struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
	Days int       `json:"days"`
	// Label is how the page and the export name it: 近 30 天, 2026-09-01 至 2026-09-30.
	Label string `json:"label"`
}

var shanghai = func() *time.Location {
	if l, err := time.LoadLocation("Asia/Shanghai"); err == nil {
		return l
	}
	return time.FixedZone("CST", 8*3600)
}()

// ParsePeriod reads a report's span: days back from now (default 30), or from and to as local
// dates (YYYY-MM-DD, both days included).
func ParsePeriod(days, from, to string, now time.Time) (Period, error) {
	now = now.In(shanghai)
	if from != "" || to != "" {
		if from == "" || to == "" {
			return Period{}, fail(400, "from 和 to 要一起给（YYYY-MM-DD）")
		}
		f, err1 := time.ParseInLocation("2006-01-02", from, shanghai)
		t, err2 := time.ParseInLocation("2006-01-02", to, shanghai)
		if err1 != nil || err2 != nil {
			return Period{}, fail(400, "日期写成 YYYY-MM-DD")
		}
		if t.Before(f) {
			return Period{}, fail(400, "from 要不晚于 to")
		}
		end := t.AddDate(0, 0, 1)
		n := int(math.Round(end.Sub(f).Hours() / 24))
		if n > 366 {
			return Period{}, fail(400, "最多一年")
		}
		return Period{From: f, To: end, Days: n, Label: from + " 至 " + to}, nil
	}
	n := 30
	if days != "" {
		v, err := strconv.Atoi(days)
		if err != nil || v < 1 || v > 366 {
			return Period{}, fail(400, "days 是 1～366")
		}
		n = v
	}
	return Period{From: now.Add(-time.Duration(n) * 24 * time.Hour), To: now, Days: n, Label: fmt.Sprintf("近 %d 天", n)}, nil
}

func (p Period) has(t time.Time) bool { return !t.Before(p.From) && t.Before(p.To) }

// Figure is one number of the report.
type Figure struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	// Value is absent when there was nothing to count it from.
	Value *float64 `json:"value"`
	// Unit: % (Value is a fraction), h, min, or what is counted (条, 次, 台, 张, 件).
	Unit string `json:"unit,omitempty"`
	// N is how many records it was counted from: the count itself for a count, the
	// denominator for a rate, the hours of data for a plant figure.
	N int `json:"n"`
	// Basis says what it was counted from.
	Basis string `json:"basis"`
	// Source, for the plant's figures: measured (from equipment) or simulated (the twin's own
	// simulation, or hours partly measured).
	Source string `json:"source,omitempty"`
}

func count(key, label, unit string, n int, basis string) Figure {
	v := float64(n)
	return Figure{Key: key, Label: label, Value: &v, Unit: unit, N: n, Basis: basis}
}

// ratio is part of whole, absent when whole is nothing.
func ratio(key, label string, part, whole int, basis string) Figure {
	f := Figure{Key: key, Label: label, Unit: "%", N: whole, Basis: basis}
	if whole > 0 {
		v := float64(part) / float64(whole)
		f.Value = &v
	}
	return f
}

// absent is a figure there is nothing to count from.
func absent(key, label, unit, basis string) Figure {
	return Figure{Key: key, Label: label, Unit: unit, Basis: basis}
}

// Text is the figure as a person reads it: "61.2%", "3.5 h", "12 条", or 没有数据.
func (f Figure) Text() string {
	if f.Value == nil {
		return "没有数据"
	}
	v := *f.Value
	switch f.Unit {
	case "%":
		return strconv.FormatFloat(v*100, 'f', 1, 64) + "%"
	case "h", "min":
		return num(v) + " " + f.Unit
	case "":
		return num(v)
	}
	return num(v) + " " + f.Unit
}

// num is a number with as many decimals as its size deserves: 1234, 31.3, 3.25, 0.04.
func num(v float64) string {
	prec := 2
	switch a := math.Abs(v); {
	case a >= 100:
		prec = 0
	case a >= 10:
		prec = 1
	}
	s := strconv.FormatFloat(v, 'f', prec, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
	}
	return s
}

// Section is a part of the report: its figures, and why it has none (Missing) or what to keep
// in mind reading them (Note).
type Section struct {
	Figures []Figure `json:"figures"`
	Missing string   `json:"missing,omitempty"`
	Note    string   `json:"note,omitempty"`
}

// Value is the report.
type Value struct {
	Company string    `json:"company"`
	Name    string    `json:"name"`
	Period  Period    `json:"period"`
	At      time.Time `json:"at"`
	// Suggestions are the advisor's plans: proposed, decided, measured.
	Suggestions Suggestions `json:"suggestions"`
	// AI is what the console agent proposed and what AI clients sent to machines.
	AI          Section    `json:"ai"`
	Calibration CalibValue `json:"calibration"`
	Orders      Section    `json:"orders"`
	Plant       PlantValue `json:"plant"`
}

// Suggestions are the advisor's plans, with each adopted one and what it changed.
type Suggestions struct {
	Section
	Plans []PlanResult `json:"plans"`
}

// PlanResult is an adopted plan and what was measured since.
type PlanResult struct {
	ID      string    `json:"id"`
	Does    string    `json:"does"`
	Metric  string    `json:"metric"`
	Label   string    `json:"label"`
	Want    string    `json:"want"` // up | down
	Adopted time.Time `json:"adopted"`
	// Baseline is the metric when the plan was adopted, Measured the latest measurement (absent:
	// not measured yet), Target what the plan promised.
	Baseline *float64 `json:"baseline,omitempty"`
	Measured *float64 `json:"measured,omitempty"`
	Target   *float64 `json:"target,omitempty"`
	// Change is (measured − baseline) / |baseline|; Better says it moved the way the plan wanted.
	Change   *float64 `json:"change,omitempty"`
	Better   *bool    `json:"better,omitempty"`
	DataDays int      `json:"dataDays"`
	// Outcome of the acceptance: achieved | missed | guard_broken | no_data; empty before it.
	Outcome  string `json:"outcome,omitempty"`
	Accepted bool   `json:"accepted"`
	// Unit is % when the metric is a share (the values are fractions).
	Unit string `json:"unit,omitempty"`
}

// CalibValue is calibration, with the replay error of each field over the machines it covers.
type CalibValue struct {
	Section
	Fields []FieldReplay `json:"fields"`
}

// FieldReplay is how far the models were from the measurements in one field over the fitted
// periods, with the template's parameters (Before) and the fitted ones (After), averaged over
// Machines machines and N readings.
type FieldReplay struct {
	Field    string  `json:"field"`
	Label    string  `json:"label"`
	Unit     string  `json:"unit"`
	Before   float64 `json:"before"`
	After    float64 `json:"after"`
	Machines int     `json:"machines"`
	N        int     `json:"n"`
}

// PlantValue is how the plant did: the period's figures, week by week, and before and after the
// first adopted plan.
type PlantValue struct {
	Section
	// Source is what the trend is drawn from: measured, simulated, or empty without data.
	Source  string   `json:"source,omitempty"`
	Weeks   []Week   `json:"weeks"`
	Compare *Compare `json:"compare,omitempty"`
}

// Week is a week of the period (Monday on), as far as it lies in the period. Values are absent
// in a week without data from the trend's source.
type Week struct {
	Start    time.Time `json:"start"`
	Days     int       `json:"days"`
	OEE      *float64  `json:"oee"`
	Output   *float64  `json:"output"`
	Downtime *float64  `json:"downtime"`
	// OutputPerDay is output per line per day of records, DowntimePerDay machine downtime hours
	// per machine per day of records: a week the period cuts short, or one in which equipment
	// reported from fewer lines, compares with the others.
	OutputPerDay   *float64 `json:"outputPerDay"`
	DowntimePerDay *float64 `json:"downtimePerDay"`
	LineHours      int      `json:"lineHours"`
	// Measured is the share of the week's line-hours that came from equipment.
	Measured *float64 `json:"measured"`
}

// Compare is the plant before and after the first adopted plan, over as many whole days each
// side as the period has and the time since allows; the adoption day itself is left out.
type Compare struct {
	Plan    string    `json:"plan"`
	Does    string    `json:"does"`
	Adopted time.Time `json:"adopted"`
	Days    int       `json:"days"`
	Source  string    `json:"source,omitempty"`
	Before  Side      `json:"before"`
	After   Side      `json:"after"`
	Note    string    `json:"note"`
}

// Side is one side of a comparison: OEE, output per line per day and downtime per machine per
// day, over the days that have records.
type Side struct {
	From           time.Time `json:"from"`
	To             time.Time `json:"to"`
	OEE            *float64  `json:"oee"`
	OutputPerDay   *float64  `json:"outputPerDay"`
	DowntimePerDay *float64  `json:"downtimePerDay"`
	Days           int       `json:"days"`
	LineHours      int       `json:"lineHours"`
}

// Value is the report for company over p: members and above.
func (o *Ops) Value(ctx context.Context, who auth.Who, company string, p Period) (*Value, error) {
	if !slices.Contains(o.Engine.CompanyIDs(), company) {
		return nil, fail(404, "没有公司 %s", company)
	}
	if !who.Can(company, auth.Member) {
		if who.Key == "" {
			return nil, fail(401, "用令牌登录")
		}
		return nil, fail(403, "要公司 %s %s以上的令牌", company, auth.Member.Label())
	}
	now := time.Now()
	v := &Value{Company: company, Name: o.companyName(company), Period: p, At: now}

	b, missing := o.board(ctx, company)
	v.Suggestions = suggestionsOf(b, p, missing)

	var counts []audit.Count
	auditMissing := ""
	if o.Audit == nil {
		auditMissing = "这台服务器不记操作（没有数据目录）"
	} else {
		var err error
		if counts, err = o.Audit.Counts(ctx, company, p.From, p.To, "agent", "command", "calib"); err != nil {
			auditMissing = "操作记录读不出来：" + err.Error()
		}
	}
	var reqs []guard.Request
	reqMissing := ""
	if o.Commands == nil {
		reqMissing = "这台服务器没有指令把关（没有数据目录）"
	} else {
		var err error
		if reqs, err = o.Commands.Between(company, p.From, p.To); err != nil {
			reqMissing = "指令请求读不出来：" + err.Error()
		}
	}
	v.AI = aiOf(counts, auditMissing, reqs, reqMissing)

	var machines []calib.Machine
	calibMissing := ""
	if o.Calib == nil {
		calibMissing = "这台服务器没有存设备读数，没有校准（没有数据目录）"
	} else {
		var err error
		if machines, err = o.Calib.Status(company); err != nil {
			calibMissing = "校准读不出来：" + err.Error()
		}
	}
	v.Calibration = calibOf(machines, counts, auditMissing, p, calibMissing)

	var orders []erp.Order
	ordersMissing := ""
	if o.Production == nil {
		ordersMissing = "这台服务器没有接 ERP（没有数据目录）"
	} else {
		var err error
		if orders, err = o.Production.Store.Span(company, p.From, p.To); err != nil {
			ordersMissing = "工单读不出来：" + err.Error()
		}
	}
	v.Orders = ordersOf(orders, p, now, ordersMissing)

	first := firstAdopted(b, p.To)
	from := p.From
	if first != nil {
		from = earlier(from, first.at.In(shanghai).AddDate(0, 0, -p.Days-1))
	}
	var days []history.Day
	plantMissing := ""
	if h := o.History[company]; h == nil {
		plantMissing = "这家公司没有接历史库，没有逐小时的产出和停机记录"
	} else {
		var err error
		if days, err = h.Days(ctx, from, p.To); err != nil {
			plantMissing = "历史库读不出来：" + err.Error()
		}
	}
	v.Plant = plantOf(days, p, first, now, plantMissing)
	return v, nil
}

// ValueTool is the report as the assistants ask for it: days back from now, or a first and last
// day.
func (o *Ops) ValueTool(ctx context.Context, who auth.Who, company, days, from, to string) (any, error) {
	p, err := ParsePeriod(days, from, to, time.Now())
	if err != nil {
		return nil, err
	}
	return o.Value(ctx, who, company, p)
}

func (o *Ops) companyName(id string) string {
	var list []struct{ ID, Name string }
	if json.Unmarshal([]byte(o.Engine.List()), &list) == nil {
		for _, c := range list {
			if c.ID == id {
				return c.Name
			}
		}
	}
	return id
}

// ---- the advisor's plans ----

// board is the part of the advisor's consulting record (GET /v1/consult) the report reads.
type board struct {
	Goals []struct {
		Plans []planView `json:"plans"`
	} `json:"goals"`
	Loose []planView `json:"loose_plans"`
}

type planView struct {
	Plan struct {
		ID     string `json:"id"`
		Does   string `json:"does"`
		At     string `json:"at"`
		Expect struct {
			Metric    string `json:"metric"`
			Direction string `json:"direction"`
		} `json:"expect"`
	} `json:"plan"`
	State    string `json:"state"`
	Decision *struct {
		Verdict  string       `json:"verdict"`
		At       string       `json:"at"`
		Baseline *measurement `json:"baseline"`
	} `json:"decision"`
	Progress   *acceptance `json:"progress"`
	Acceptance *acceptance `json:"acceptance"`
}

type acceptance struct {
	Final    bool         `json:"final"`
	Baseline *float64     `json:"baseline"`
	Target   *float64     `json:"target"`
	Measured *measurement `json:"measured"`
	Outcome  string       `json:"outcome"`
	At       string       `json:"at"`
}

type measurement struct {
	Value    *float64 `json:"value"`
	DataDays float64  `json:"data_days"`
}

func (b *board) plans() []planView {
	var out []planView
	for _, g := range b.Goals {
		out = append(out, g.Plans...)
	}
	return append(out, b.Loose...)
}

// board reads the company's consulting record, or says why there is none.
func (o *Ops) board(ctx context.Context, company string) (*board, string) {
	if o.Consult == nil {
		return nil, ErrNoAdvisor.Error()
	}
	raw, err := o.Consult(ctx, company)
	if errors.Is(err, ErrNoAdvisor) {
		return nil, err.Error()
	}
	if err != nil {
		return nil, "顾问服务读不出来：" + err.Error()
	}
	var b board
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, "顾问服务的回答看不懂：" + err.Error()
	}
	return &b, ""
}

// when reads the advisor's times: RFC 3339, or a local date and time.
func when(s string) (time.Time, bool) {
	for _, l := range []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.ParseInLocation(l, s, shanghai); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

const planBasis = "顾问服务的咨询档案"

func suggestionsOf(b *board, p Period, missing string) Suggestions {
	keys := []struct{ key, label, unit string }{
		{"plans.proposed", "提出的计划", "条"}, {"plans.adopted", "采纳", "条"}, {"plans.rejected", "否决", "条"},
		{"plans.adoption", "采纳率", "%"}, {"plans.decision_time", "从提出到决定（中位数）", "h"}, {"plans.waiting", "到现在还没决定", "条"},
		{"plans.measured", "采纳后量过效果的", "条"}, {"plans.better", "量出来往计划方向变了的", "%"}, {"plans.accepted", "已验收", "条"}, {"plans.achieved", "验收达成", "%"},
	}
	if b == nil {
		s := Suggestions{Section: Section{Missing: missing}, Plans: []PlanResult{}}
		for _, k := range keys {
			s.Figures = append(s.Figures, absent(k.key, k.label, k.unit, planBasis))
		}
		return s
	}
	var proposed, adopted, rejected, waiting int
	var hours []float64
	plans := []PlanResult{}
	for _, pv := range b.plans() {
		at, ok := when(pv.Plan.At)
		if ok && p.has(at) {
			proposed++
		}
		if pv.Decision == nil {
			if pv.State == "proposed" && (!ok || at.Before(p.To)) {
				waiting++
			}
			continue
		}
		dat, dok := when(pv.Decision.At)
		inPeriod := dok && p.has(dat)
		if inPeriod {
			switch pv.Decision.Verdict {
			case "adopted":
				adopted++
			case "rejected":
				rejected++
			}
			if ok && !dat.Before(at) {
				hours = append(hours, dat.Sub(at).Hours())
			}
		}
		if pv.Decision.Verdict != "adopted" || !dok || !dat.Before(p.To) {
			continue
		}
		// an adopted plan belongs to the period when it was adopted in it, or measured in it
		a := cmp.Or(pv.Acceptance, pv.Progress)
		measuredIn := false
		if a != nil {
			if t, ok := when(a.At); ok && p.has(t) {
				measuredIn = true
			}
		}
		if !inPeriod && !measuredIn {
			continue
		}
		plans = append(plans, planResult(pv, dat))
	}
	measured, better, accepted, achieved := 0, 0, 0, 0
	for _, r := range plans {
		if r.Measured != nil && r.Baseline != nil {
			measured++
			if r.Better != nil && *r.Better {
				better++
			}
		}
		if r.Accepted {
			accepted++
			if r.Outcome == "achieved" {
				achieved++
			}
		}
	}
	decided := adopted + rejected
	s := Suggestions{Plans: plans}
	s.Figures = []Figure{
		count("plans.proposed", "提出的计划", "条", proposed, planBasis+"：提出时间在这段时间里的计划"),
		count("plans.adopted", "采纳", "条", adopted, planBasis+"：这段时间里做出的采纳决定"),
		count("plans.rejected", "否决", "条", rejected, planBasis+"：这段时间里做出的否决决定"),
		ratio("plans.adoption", "采纳率", adopted, decided, planBasis+"：这段时间里的决定中采纳的比例"),
		medianHours("plans.decision_time", "从提出到决定（中位数）", hours, planBasis+"：这段时间里做出的决定，计划提出到有人采纳或否决"),
		count("plans.waiting", "到现在还没决定", "条", waiting, planBasis+"：提出了、还没人采纳或否决的计划"),
		count("plans.measured", "采纳后量过效果的", "条", measured, planBasis+"：这段时间里采纳或量过的计划中，有基线也有量出值的"),
		ratio("plans.better", "量出来往计划方向变了的", better, measured, planBasis+"：量出值和基线比，朝计划要的方向变了；不等于达成目标"),
		count("plans.accepted", "已验收", "条", accepted, planBasis+"：到期后正式验收的计划"),
		ratio("plans.achieved", "验收达成", achieved, accepted, planBasis+"：验收结论是达成的比例"),
	}
	if len(plans) > 0 {
		s.Note = "每条计划的变化只和它自己的基线比；指标不同，不加总。"
	}
	return s
}

func medianHours(key, label string, hours []float64, basis string) Figure {
	f := Figure{Key: key, Label: label, Unit: "h", N: len(hours), Basis: basis}
	if len(hours) > 0 {
		v := math.Round(median(hours)*10) / 10
		f.Value = &v
	}
	return f
}

func median(xs []float64) float64 {
	s := slices.Clone(xs)
	slices.Sort(s)
	if n := len(s); n%2 == 1 {
		return s[n/2]
	} else {
		return (s[n/2-1] + s[n/2]) / 2
	}
}

func planResult(pv planView, adopted time.Time) PlanResult {
	r := PlanResult{ID: pv.Plan.ID, Does: pv.Plan.Does, Metric: pv.Plan.Expect.Metric, Label: metricLabel(pv.Plan.Expect.Metric),
		Want: pv.Plan.Expect.Direction, Adopted: adopted}
	if d := pv.Decision; d.Baseline != nil {
		r.Baseline = d.Baseline.Value
	}
	a := pv.Acceptance
	if a != nil {
		r.Accepted, r.Outcome = true, a.Outcome
	} else {
		a = pv.Progress
	}
	if a != nil {
		if a.Baseline != nil {
			r.Baseline = a.Baseline
		}
		r.Target = a.Target
		if a.Measured != nil && a.Measured.Value != nil {
			r.Measured, r.DataDays = a.Measured.Value, int(a.Measured.DataDays)
		}
	}
	if r.Baseline != nil && r.Measured != nil {
		diff := *r.Measured - *r.Baseline
		if *r.Baseline != 0 {
			c := diff / math.Abs(*r.Baseline)
			r.Change = &c
		}
		better := diff > 0 && r.Want == "up" || diff < 0 && r.Want == "down"
		r.Better = &better
	}
	if isShare(r.Metric, r.Baseline, r.Measured, r.Target) {
		r.Unit = "%"
	}
	return r
}

var model = func() *semantic.Model {
	m, _ := semantic.Load(history.Model)
	return m
}()

// metricLabel is a metric as people say it: its first synonym in the history's model.
func metricLabel(name string) string {
	if model != nil {
		for _, m := range model.Metrics {
			if m.Name == name && len(m.Synonyms) > 0 {
				return m.Synonyms[0]
			}
		}
	}
	return name
}

// isShare: the metric is a ratio of the model and its values are fractions.
func isShare(name string, vs ...*float64) bool {
	if model == nil || model.Metric(name) == nil || model.Additivity(name) != "non_additive" {
		return false
	}
	for _, v := range vs {
		if v != nil && (*v < 0 || *v > 1) {
			return false
		}
	}
	return true
}

// adoption is the first plan adopted.
type adoption struct {
	plan, does string
	at         time.Time
}

func firstAdopted(b *board, before time.Time) *adoption {
	if b == nil {
		return nil
	}
	var first *adoption
	for _, pv := range b.plans() {
		if pv.Decision == nil || pv.Decision.Verdict != "adopted" {
			continue
		}
		t, ok := when(pv.Decision.At)
		if !ok || !t.Before(before) || first != nil && !t.Before(first.at) {
			continue
		}
		first = &adoption{plan: pv.Plan.ID, does: pv.Plan.Does, at: t}
	}
	return first
}

// ---- the AI's actions ----

const commandBasis = "活动记录里经 MCP 和助手发的设备指令"
const requestBasis = "指令把关的请求：AI 发的、要人批准的指令"

func aiOf(counts []audit.Count, auditMissing string, reqs []guard.Request, reqMissing string) Section {
	s := Section{}
	sum := func(action string, ok *bool, ai bool) int {
		n := 0
		for _, c := range counts {
			if c.Action == action && (ok == nil || c.OK == *ok) && (!ai || c.Via == "mcp" || c.Via == "assistant") {
				n += c.N
			}
		}
		return n
	}
	yes, no := true, false
	const agentBasis = "活动记录：工作台助手提出的改动卡片"
	if auditMissing != "" {
		for _, k := range [][3]string{{"agent.proposed", "助手提出的改动", "条"}, {"agent.confirmed", "确认执行", "条"}, {"agent.dismissed", "不要", "条"},
			{"agent.confirm_rate", "确认率", "%"}, {"agent.open", "没有回应", "条"}, {"commands.direct", "AI 直接下发的指令", "条"}} {
			s.Figures = append(s.Figures, absent(k[0], k[1], k[2], agentBasis))
		}
		s.Missing = auditMissing
	} else {
		proposed, confirmed, failed, dismissed := sum("agent.propose", nil, false), sum("agent.confirm", &yes, false), sum("agent.confirm", &no, false), sum("agent.dismiss", nil, false)
		s.Figures = append(s.Figures,
			count("agent.proposed", "助手提出的改动", "条", proposed, agentBasis+"（agent.propose）"),
			count("agent.confirmed", "确认执行", "条", confirmed, agentBasis+"：有人点了确认、执行成功的（agent.confirm）"),
			count("agent.dismissed", "不要", "条", dismissed, agentBasis+"：有人点了不要的（agent.dismiss）"),
			ratio("agent.confirm_rate", "确认率", confirmed, confirmed+failed+dismissed, agentBasis+"：有回应的卡片里确认的比例"),
			count("agent.open", "没有回应", "条", max(0, proposed-confirmed-failed-dismissed), agentBasis+"：提出了，既没确认也没点不要"),
			count("commands.direct", "AI 直接下发的指令", "条", sum("command", &yes, true), commandBasis+"：没等人、直接执行成功的"),
		)
		if failed > 0 {
			s.Note = fmt.Sprintf("另有 %d 条确认了但没执行成功。", failed)
		}
	}
	var status = map[string]int{}
	var minutes []float64
	for _, r := range reqs {
		status[r.Status]++
		if (r.Status == "sent" || r.Status == "denied" || r.Status == "refused" || r.Status == "failed") && r.DecidedBy != "" && r.DecidedAt != nil {
			minutes = append(minutes, r.DecidedAt.Sub(r.Created).Minutes())
		}
	}
	if reqMissing != "" {
		for _, k := range [][3]string{{"commands.held", "等人批准的指令", "条"}, {"commands.approved", "批准后执行", "条"}, {"commands.denied", "被人拒绝", "条"},
			{"commands.expired", "没人批准、过期", "条"}, {"commands.waiting", "还在等", "条"}, {"commands.decision_time", "从发出到有人批准或拒绝（中位数）", "min"}} {
			s.Figures = append(s.Figures, absent(k[0], k[1], k[2], requestBasis))
		}
		s.Missing = strings.TrimPrefix(s.Missing+"；"+reqMissing, "；")
	} else {
		m := Figure{Key: "commands.decision_time", Label: "从发出到有人批准或拒绝（中位数）", Unit: "min", N: len(minutes), Basis: requestBasis + "：有人批准或拒绝的"}
		if len(minutes) > 0 {
			v := math.Round(median(minutes)*10) / 10
			m.Value = &v
		}
		s.Figures = append(s.Figures,
			count("commands.held", "等人批准的指令", "条", len(reqs), requestBasis),
			count("commands.approved", "批准后执行", "条", status["sent"], requestBasis+"：有人批准、已下到设备的"),
			count("commands.denied", "被人拒绝", "条", status["denied"], requestBasis+"：有人拒绝的"),
			count("commands.expired", "没人批准、过期", "条", status["expired"], requestBasis+"：时限内没人处理的"),
			count("commands.waiting", "还在等", "条", status["waiting"]+status["deciding"], requestBasis+"：现在还没人处理的"),
			m,
		)
	}
	// refused by rule: sent straight and refused, or refused when approved (the machine had changed)
	refused := Figure{Key: "commands.refused", Label: "被规则拒绝", Unit: "条", Basis: commandBasis + "被规则拒绝的（command.refused），加批准时设备已变、规则不让下的"}
	failed := Figure{Key: "commands.failed", Label: "下发失败", Unit: "条", Basis: commandBasis + "执行失败的，加批准后执行失败的"}
	if auditMissing == "" || reqMissing == "" {
		r := float64(sum("command.refused", nil, true) + status["refused"])
		f := float64(sum("command", &no, true) + status["failed"])
		refused.Value, refused.N = &r, int(r)
		failed.Value, failed.N = &f, int(f)
	}
	s.Figures = append(s.Figures, refused, failed)
	return s
}

// ---- calibration ----

var fieldInfo = map[string][2]string{"temp": {"温度", "°C"}, "power": {"功率", "kW"}, "vib": {"振动", "mm/s"}}

func calibOf(ms []calib.Machine, counts []audit.Count, auditMissing string, p Period, missing string) CalibValue {
	const basis = "仿真校准：每台设备最近一次拟合和在用的参数"
	keys := [][3]string{{"calib.in_force", "在用校准的设备", "台"}, {"calib.applied", "这段时间应用了校准", "台"}, {"calib.fitted", "这段时间拟合过", "台"},
		{"calib.apply", "应用操作", "次"}, {"calib.revert", "撤销操作", "次"}}
	if missing != "" {
		v := CalibValue{Section: Section{Missing: missing}, Fields: []FieldReplay{}}
		for _, k := range keys {
			v.Figures = append(v.Figures, absent(k[0], k[1], k[2], basis))
		}
		return v
	}
	inForce, applied, fitted := 0, 0, 0
	type acc struct {
		before, after float64
		machines, n   int
	}
	fields := map[string]*acc{}
	var used []calib.Machine
	for _, m := range ms {
		if m.AppliedAt != nil {
			inForce++
			used = append(used, m)
			if p.has(*m.AppliedAt) {
				applied++
			}
		}
		if m.FittedAt != nil && p.has(*m.FittedAt) {
			fitted++
		}
	}
	note := "回放误差：拟合用的那段读数，按模板参数和按拟合参数各算一遍，模型和实测的平均差；只算在用校准的设备。"
	if len(used) == 0 {
		// nothing in force: what the fits would do, said as such
		for _, m := range ms {
			if m.FittedAt != nil && p.has(*m.FittedAt) {
				used = append(used, m)
			}
		}
		note = "还没有设备在用校准；下面的回放误差是这段时间拟合过、还没应用的设备。"
	}
	for _, m := range used {
		for _, r := range m.Replay {
			a := fields[r.Field]
			if a == nil {
				a = &acc{}
				fields[r.Field] = a
			}
			a.before += r.Before
			a.after += r.After
			a.machines++
			a.n += r.N
		}
	}
	out := CalibValue{Fields: []FieldReplay{}}
	for _, f := range []string{"temp", "power", "vib"} {
		if a := fields[f]; a != nil {
			out.Fields = append(out.Fields, FieldReplay{Field: f, Label: fieldInfo[f][0], Unit: fieldInfo[f][1],
				Before: round3(a.before / float64(a.machines)), After: round3(a.after / float64(a.machines)), Machines: a.machines, N: a.n})
		}
	}
	if len(out.Fields) > 0 {
		out.Note = note
	}
	logged := func(action, label string) Figure {
		if auditMissing != "" {
			return absent(action, label, "次", "活动记录："+action)
		}
		n := 0
		for _, c := range counts {
			if c.Action == action && c.OK {
				n += c.N
			}
		}
		return count(action, label, "次", n, "活动记录："+action)
	}
	out.Figures = []Figure{
		count("calib.in_force", "在用校准的设备", "台", inForce, basis+"：现在在用拟合参数的"),
		count("calib.applied", "这段时间应用了校准", "台", applied, basis+"：应用时间在这段时间里的"),
		count("calib.fitted", "这段时间拟合过", "台", fitted, basis+"：最近一次拟合在这段时间里的"),
		logged("calib.apply", "应用操作"),
		logged("calib.revert", "撤销操作"),
	}
	return out
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

func earlier(a, b time.Time) time.Time {
	if b.Before(a) {
		return b
	}
	return a
}

// ---- orders ----

func ordersOf(orders []erp.Order, p Period, now time.Time, missing string) Section {
	const basis = "ERP 下达的生产工单"
	keys := [][3]string{{"orders.received", "收到的工单", "张"}, {"orders.finished", "完工", "张"}, {"orders.on_time", "按期完工", "张"},
		{"orders.late", "延期完工", "张"}, {"orders.on_time_rate", "按期完工率", "%"}, {"orders.overdue", "现在已过交期还没完工", "张"}}
	if missing != "" {
		s := Section{Missing: missing}
		for _, k := range keys {
			s.Figures = append(s.Figures, absent(k[0], k[1], k[2], basis))
		}
		return s
	}
	received, finished, onTime, late, noDue, overdue := 0, 0, 0, 0, 0, 0
	for _, o := range orders {
		if p.has(o.Created) {
			received++
		}
		if o.Finished != nil && p.has(*o.Finished) {
			finished++
			switch {
			case o.Due == nil:
				noDue++
			case o.Finished.After(*o.Due):
				late++
			default:
				onTime++
			}
		}
		if (o.Status == erp.Queued || o.Status == erp.Running) && o.Due != nil && now.After(*o.Due) {
			overdue++
		}
	}
	s := Section{Figures: []Figure{
		count("orders.received", "收到的工单", "张", received, basis+"：收到时间在这段时间里的"),
		count("orders.finished", "完工", "张", finished, basis+"：完工时间在这段时间里的"),
		count("orders.on_time", "按期完工", "张", onTime, basis+"：完工时间不晚于交期的"),
		count("orders.late", "延期完工", "张", late, basis+"：完工时间晚于交期的"),
		ratio("orders.on_time_rate", "按期完工率", onTime, onTime+late, basis+"：这段时间完工、有交期的工单中按期的比例"),
		count("orders.overdue", "现在已过交期还没完工", "张", overdue, basis+"：排队或在做、交期已过的"),
	}}
	if noDue > 0 {
		s.Note = fmt.Sprintf("另有 %d 张完工的工单没有交期，不算进按期完工率。", noDue)
	}
	return s
}

// ---- the plant ----

// tally adds history days up.
type tally struct {
	lineHours, measuredHours, machineHours int
	planned, goodIdeal, down               float64
	produced                               int
	days                                   map[time.Time]bool
}

func (t *tally) add(d history.Day) {
	t.lineHours += d.LineHours
	if d.Source == "measured" {
		t.measuredHours += d.LineHours
	}
	t.machineHours += d.MachineHours
	t.planned += d.PlannedS
	t.goodIdeal += d.GoodIdealS
	t.down += d.DownS
	t.produced += d.Produced
	if d.LineHours > 0 {
		if t.days == nil {
			t.days = map[time.Time]bool{}
		}
		t.days[d.Day] = true
	}
}

func (t *tally) oee() *float64 {
	if t.planned <= 0 {
		return nil
	}
	v := round3(t.goodIdeal / t.planned)
	return &v
}

func (t *tally) output() *float64 {
	if t.lineHours == 0 {
		return nil
	}
	v := float64(t.produced)
	return &v
}

func (t *tally) downtime() *float64 {
	if t.machineHours == 0 {
		return nil
	}
	v := math.Round(t.down/3600*10) / 10
	return &v
}

// lineDay is output per line per day of its records: a day still running, or a week in which
// equipment reported from fewer lines, compares with a whole one.
func (t *tally) lineDay() *float64 {
	if t.lineHours == 0 {
		return nil
	}
	x := math.Round(float64(t.produced)*24/float64(t.lineHours)*10) / 10
	return &x
}

// machineDay is downtime hours per machine per day of its records.
func (t *tally) machineDay() *float64 {
	if t.machineHours == 0 {
		return nil
	}
	x := math.Round(t.down/3600*24/float64(t.machineHours)*100) / 100
	return &x
}

// localDay is a history day as a local midnight.
func localDay(d time.Time) time.Time {
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, shanghai)
}

func plantOf(days []history.Day, p Period, first *adoption, now time.Time, missing string) PlantValue {
	const basis = "历史库：各产线、各设备逐小时的记录"
	keys := [][3]string{{"plant.oee", "OEE", "%"}, {"plant.output", "产量", "件"}, {"plant.downtime", "设备停机（故障、保养、换模）", "h"}, {"plant.measured", "数据实测占比", "%"}}
	if missing != "" {
		v := PlantValue{Section: Section{Missing: missing}, Weeks: []Week{}}
		for _, k := range keys {
			v.Figures = append(v.Figures, absent(k[0], k[1], k[2], basis))
		}
		return v
	}
	real := func(d history.Day) bool { return d.Source == "measured" }
	all := func(history.Day) bool { return true }
	in := func(d history.Day) bool {
		t := localDay(d.Day)
		return !t.Before(localDay(p.From.In(shanghai))) && t.Before(p.To)
	}
	var period, measured tally
	for _, d := range days {
		if in(d) {
			period.add(d)
			if real(d) {
				measured.add(d)
			}
		}
	}
	v := PlantValue{Weeks: []Week{}}
	use, t := all, &period
	switch {
	case period.lineHours == 0 && period.machineHours == 0:
		v.Note = "这段时间历史库里没有记录。"
	case measured.lineHours > 0:
		v.Source, use, t = "measured", real, &measured
		v.Note = "只算设备实测的小时；仿真或部分实测的小时不算。"
	default:
		v.Source = "simulated"
		v.Note = "这段时间没有设备实测的数据，下面全是孪生仿真（或部分实测）的数字，不代表真实工厂。"
	}
	src := v.Source
	measuredShare := Figure{Key: "plant.measured", Label: "数据实测占比", Unit: "%", N: period.lineHours, Basis: basis + "：设备实测的产线小时占全部产线小时"}
	if period.lineHours > 0 {
		x := round3(float64(period.measuredHours) / float64(period.lineHours))
		measuredShare.Value = &x
	}
	v.Figures = []Figure{
		{Key: "plant.oee", Label: "OEE", Value: t.oee(), Unit: "%", N: t.lineHours, Basis: basis + "：合格件按理论节拍折算的时间 / 计划时间，按产线小时", Source: src},
		{Key: "plant.output", Label: "产量", Value: t.output(), Unit: "件", N: t.lineHours, Basis: basis + "：各产线完成加工的件数", Source: src},
		{Key: "plant.downtime", Label: "设备停机（故障、保养、换模）", Value: t.downtime(), Unit: "h", N: t.machineHours, Basis: basis + "：各台设备故障、保养、换模的小时数相加", Source: src},
		measuredShare,
	}
	// the weeks, Monday on, cut to the period
	start := localDay(p.From.In(shanghai))
	monday := start.AddDate(0, 0, -((int(start.Weekday()) + 6) % 7))
	for w := monday; w.Before(p.To); w = w.AddDate(0, 0, 7) {
		end := w.AddDate(0, 0, 7)
		lo, hi := w, earlier(end, p.To)
		if lo.Before(start) {
			lo = start
		}
		var wt, all tally
		for _, d := range days {
			t := localDay(d.Day)
			if t.Before(lo) || !t.Before(hi) {
				continue
			}
			all.add(d)
			if use(d) {
				wt.add(d)
			}
		}
		wk := Week{Start: w, Days: int(math.Ceil(hi.Sub(lo).Hours() / 24)), OEE: wt.oee(), Output: wt.output(), Downtime: wt.downtime(),
			OutputPerDay: wt.lineDay(), DowntimePerDay: wt.machineDay(), LineHours: wt.lineHours}
		if all.lineHours > 0 {
			x := round3(float64(all.measuredHours) / float64(all.lineHours))
			wk.Measured = &x
		}
		v.Weeks = append(v.Weeks, wk)
	}
	if first != nil {
		v.Compare = compareAround(days, p, first, now)
	}
	return v
}

// compareAround puts the whole days before the first adoption beside as many after it.
func compareAround(days []history.Day, p Period, first *adoption, now time.Time) *Compare {
	d := localDay(first.at.In(shanghai))
	today := localDay(now.In(shanghai))
	n := min(p.Days, int(math.Round(today.Sub(d).Hours()/24)))
	c := &Compare{Plan: first.plan, Does: first.does, Adopted: first.at, Days: max(n, 0),
		Note: "前后对比只说明这两段时间里同时发生的变化，不证明是这条计划带来的：订单、人员、设备状态也在变。"}
	if n < 1 {
		c.Note = "采纳到现在还没有一整天，没法比。"
		return c
	}
	bFrom, aFrom, aTo := d.AddDate(0, 0, -n), d.AddDate(0, 0, 1), d.AddDate(0, 0, 1+n)
	sides := func(keep func(history.Day) bool) (before, after tally) {
		for _, x := range days {
			if !keep(x) {
				continue
			}
			t := localDay(x.Day)
			switch {
			case !t.Before(bFrom) && t.Before(d):
				before.add(x)
			case !t.Before(aFrom) && t.Before(aTo):
				after.add(x)
			}
		}
		return
	}
	before, after := sides(func(x history.Day) bool { return x.Source == "measured" })
	c.Source = "measured"
	if before.lineHours == 0 || after.lineHours == 0 {
		before, after = sides(func(history.Day) bool { return true })
		c.Source = "simulated"
		c.Note = "两边没有都有设备实测的数据，下面是孪生仿真（或部分实测）的数字，不代表真实工厂。" + c.Note
	}
	switch {
	case before.lineHours == 0 && after.lineHours == 0:
		c.Source, c.Note = "", "采纳前后这两段时间历史库里都没有记录，没法比。"
	case before.lineHours == 0:
		c.Source, c.Note = "", "采纳前这段时间历史库里没有记录，没法比。"
	case after.lineHours == 0:
		c.Source, c.Note = "", "采纳后这段时间历史库里没有记录，没法比。"
	}
	side := func(t tally, from, to time.Time) Side {
		nd := len(t.days)
		return Side{From: from, To: to, OEE: t.oee(), OutputPerDay: t.lineDay(), DowntimePerDay: t.machineDay(), Days: nd, LineHours: t.lineHours}
	}
	c.Before, c.After = side(before, bFrom, d), side(after, aFrom, aTo)
	return c
}

// ---- the export ----

// CSV is the report as a table a spreadsheet opens: one row per figure, then the plans and the
// weeks.
func (v *Value) CSV() []byte {
	var b bytes.Buffer
	b.WriteString("\xef\xbb\xbf") // a byte order mark, so a spreadsheet reads it as UTF-8
	w := csv.NewWriter(&b)
	w.Write([]string{"SmartFactory 成效报告", v.Name, v.Period.Label, v.Period.From.In(shanghai).Format("2006-01-02") + " 至 " + v.Period.To.In(shanghai).Add(-time.Second).Format("2006-01-02"), "生成于 " + v.At.In(shanghai).Format("2006-01-02 15:04")})
	w.Write([]string{"部分", "指标", "数值", "n", "数据来源", "依据"})
	part := func(name string, s Section) {
		for _, f := range s.Figures {
			w.Write([]string{name, f.Label, f.Text(), strconv.Itoa(f.N), sourceLabel(f.Source), f.Basis})
		}
		if s.Missing != "" {
			w.Write([]string{name, "说明", s.Missing})
		}
		if s.Note != "" {
			w.Write([]string{name, "说明", s.Note})
		}
	}
	part("建议", v.Suggestions.Section)
	part("AI 操作", v.AI)
	part("仿真校准", v.Calibration.Section)
	for _, f := range v.Calibration.Fields {
		w.Write([]string{"仿真校准", f.Label + "回放误差（模板 → 拟合）", fmt.Sprintf("%s → %s %s", num(f.Before), num(f.After), f.Unit), strconv.Itoa(f.N), "", fmt.Sprintf("%d 台设备", f.Machines)})
	}
	part("工单", v.Orders)
	part("工厂", v.Plant.Section)
	if len(v.Suggestions.Plans) > 0 {
		w.Write(nil)
		w.Write([]string{"采纳的计划", "做什么", "指标", "采纳时间", "基线", "量出值", "变化", "验收"})
		for _, p := range v.Suggestions.Plans {
			w.Write([]string{p.ID, p.Does, p.Label, p.Adopted.In(shanghai).Format("2006-01-02"), share(p.Baseline, p.Unit), share(p.Measured, p.Unit), change(p.Change), outcomeLabel[p.Outcome]})
		}
	}
	if len(v.Plant.Weeks) > 0 {
		w.Write(nil)
		w.Write([]string{"周（周一起）", "天数", "OEE", "产量（件）", "每条线日均产量（件）", "设备停机（h）", "每台设备日均停机（h）", "产线小时", "实测占比", "数据来源"})
		for _, wk := range v.Plant.Weeks {
			w.Write([]string{wk.Start.Format("2006-01-02"), strconv.Itoa(wk.Days), share(wk.OEE, "%"), share(wk.Output, ""), share(wk.OutputPerDay, ""), share(wk.Downtime, ""), share(wk.DowntimePerDay, ""), strconv.Itoa(wk.LineHours), share(wk.Measured, "%"), sourceLabel(v.Plant.Source)})
		}
	}
	if c := v.Plant.Compare; c != nil {
		w.Write(nil)
		w.Write([]string{"第一条采纳的计划前后", c.Plan + " " + c.Does, "采纳于 " + c.Adopted.In(shanghai).Format("2006-01-02"), fmt.Sprintf("各 %d 天", c.Days), sourceLabel(c.Source)})
		w.Write([]string{"", "OEE", "每条线日均产量（件）", "每台设备日均停机（h）", "有数据的天数", "产线小时"})
		for _, s := range []struct {
			name string
			x    Side
		}{{"采纳前", c.Before}, {"采纳后", c.After}} {
			w.Write([]string{s.name, share(s.x.OEE, "%"), share(s.x.OutputPerDay, ""), share(s.x.DowntimePerDay, ""), strconv.Itoa(s.x.Days), strconv.Itoa(s.x.LineHours)})
		}
		w.Write([]string{"说明", c.Note})
	}
	w.Flush()
	return b.Bytes()
}

var outcomeLabel = map[string]string{"": "未验收", "achieved": "达成", "missed": "未达成", "guard_broken": "护栏破了", "no_data": "无数据"}

func sourceLabel(s string) string {
	switch s {
	case "measured":
		return "实测"
	case "simulated":
		return "仿真"
	}
	return ""
}

func share(v *float64, unit string) string {
	if v == nil {
		return "没有数据"
	}
	if unit == "%" {
		return strconv.FormatFloat(*v*100, 'f', 1, 64) + "%"
	}
	return num(*v)
}

func change(v *float64) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%+.1f%%", *v*100)
}
