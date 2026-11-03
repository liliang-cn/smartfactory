package plan

import (
	"cmp"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"smartfactory/internal/engine"
	"smartfactory/internal/erp"
	"smartfactory/internal/stores"
)

// Service makes, keeps and publishes the companies' production plans, from the twin's lines,
// the ERP's orders, the stores' material and the die ledger.
type Service struct {
	Store  *Store
	Eng    *engine.Engine
	Orders *erp.Service
	// Stores are the stores and dies (nil: material and dies are not planned for).
	Stores *stores.Service
	Log    *slog.Logger
	// HistoryOEE is a company's measured OEE of the last days from its history database, for
	// lines the twin has no figures of yet (nil, or ok false, without one).
	HistoryOEE func(company string) (float64, bool)

	mu   sync.Mutex
	runs map[string]*whatIfJob
}

func New(st *Store, eng *engine.Engine, orders *erp.Service, log *slog.Logger) *Service {
	return &Service{Store: st, Eng: eng, Orders: orders, Log: log, runs: map[string]*whatIfJob{}}
}

// Problems are what the service refuses, and why.
type Problems struct{ List []string }

func (p *Problems) Error() string { return strings.Join(p.List, "；") }

func refuse(format string, a ...any) error {
	return &Problems{List: []string{fmt.Sprintf(format, a...)}}
}

func newID() string {
	b := make([]byte, 6)
	rand.Read(b)
	return "P" + time.Now().Format("060102-1504") + "-" + hex.EncodeToString(b[:2])
}

func norm(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// names are a name and its words ("KFR-35GW 挂机内机": the model alone is a name too).
func names(s string) []string { return append([]string{norm(s)}, strings.Fields(norm(s))...) }

// makes is whether a line makes a part number or product, as the order router decides it.
func makes(l engine.PlanLine, want string) bool {
	want = norm(want)
	if want == "" {
		return false
	}
	cands := []string{norm(l.Product)}
	cands = append(cands, names(l.ProductName)...)
	for _, p := range l.Parts {
		cands = append(cands, norm(p.PartNo))
		cands = append(cands, names(p.Name)...)
	}
	return slices.Contains(cands, want)
}

// Lines are the company's lines as the scheduler sees them: rate, changeover, calendar, what runs now.
func (s *Service) lines(company string, m *engine.PlanModel, running map[string]erp.Order) []Line {
	hist, histOK := 0.0, false
	if s.HistoryOEE != nil {
		hist, histOK = s.HistoryOEE(company)
	}
	var out []Line
	for _, p := range m.Plants {
		cal := calendarOf(p.Calendar)
		for _, l := range p.Lines {
			x := Line{Key: p.ID + "/" + l.ID, Plant: p.ID, PlantName: p.Name, ID: l.ID, Name: l.Name, No: l.No, Neck: l.Neck, Kinds: l.Tools,
				Last: l.Last, Down: l.Down, Crew: l.Crew, Cal: cal, Product: l.ProductName, Family: []string{l.Product, l.ProductName}}
			switch {
			case l.Span >= 1800 && l.Perf >= 0.3:
				x.Eff = math.Min(1, l.Perf*math.Max(l.Quality, 0.5))
				x.RateSource = fmt.Sprintf("孪生近 %.0f 小时实绩：性能 %.0f%% × 合格率 %.0f%%", l.Span/3600, l.Perf*100, l.Quality*100)
			case histOK && hist > 0.2:
				x.Eff = math.Min(1, hist)
				x.RateSource = fmt.Sprintf("历史库近 7 天 OEE %.0f%%", hist*100)
			case l.PlanEff > 0.2:
				x.Eff = math.Min(1, l.PlanEff)
				x.RateSource = fmt.Sprintf("近期工单实际效率 %.0f%%", l.PlanEff*100)
			default:
				x.Eff = 0.85
				x.RateSource = "还没有实绩，按 85% 效率"
			}
			x.Rate = l.Neck / x.Eff
			x.RateSource = fmt.Sprintf("节拍 %.0f 秒 ÷ %s", l.Neck, x.RateSource)
			switch {
			case l.Changeover != nil:
				x.Setup, x.SetupSource = *l.Changeover*60, "配置的换型时间"
			case l.ToolChange > 0:
				x.Setup, x.SetupSource = l.ToolChange, "设备换模时间（最慢的一台）"
			case len(l.Parts) > 1:
				x.Setup, x.SetupSource = 30*60, "没配换型时间，按 30 分钟"
			default:
				x.Setup, x.SetupSource = 0, "只做一个料号，不换型"
			}
			if r := l.Running; r != nil {
				run := &Running{No: r.No, Part: r.Part, Left: max(0, r.Qty-r.Good)}
				if o, ok := running[r.No]; ok {
					run.Name, run.Customer = o.Name, o.Customer
					if o.Due != nil {
						run.Due = *o.Due
					}
				}
				x.Running = run
			}
			out = append(out, x)
		}
	}
	return out
}

func calendarOf(c engine.PlanCalendar) *Calendar {
	out := &Calendar{Off: c.Off}
	for _, b := range c.Breaks {
		out.Breaks = append(out.Breaks, Break{At: b.At, Minutes: b.Minutes, Label: b.Label})
	}
	if sh := c.Shifts; sh != nil {
		out.Shifts, out.Handover, out.Pace = sh.At, sh.Handover, sh.Pace
	}
	if o := c.Open; o != nil {
		out.Open = &[2]float64{o.From, o.To}
	}
	if pm := c.PM; pm != nil {
		out.PM = &[2]float64{pm.Hours, pm.Start}
	}
	return out
}

// input is what a plan of the company is made from now: its lines, its queued orders (with the
// lines that make each, material and dies), the settings.
func (s *Service) input(company string, st Settings, goal Goal) (*Input, error) {
	m := s.Eng.PlanModel(company)
	if m == nil {
		return nil, refuse("公司 %s 没在运行", company)
	}
	list, err := s.Orders.Store.List(company, erp.Filter{Status: []string{erp.Queued, erp.Running}, Limit: 1000})
	if err != nil {
		return nil, err
	}
	running := map[string]erp.Order{}
	for _, o := range list {
		if o.Status == erp.Running {
			running[o.No] = o
		}
	}
	wants, err := s.Orders.Store.Wants(company)
	if err != nil {
		return nil, err
	}
	in := &Input{Now: time.UnixMilli(m.Now), Goal: goal, Lines: s.lines(company, m, running), Stops: st.Stops}
	byKey := map[string]engine.PlanLine{}
	plantOf := map[string]engine.PlanPlant{}
	for _, p := range m.Plants {
		for _, l := range p.Lines {
			byKey[p.ID+"/"+l.ID] = l
			plantOf[p.ID+"/"+l.ID] = p
		}
	}
	// what each order lacks, the orders in due-date order taking the stock in turn
	short := map[string]string{}
	if s.Stores != nil {
		var ds []stores.Demand
		qs := slices.Clone(list)
		slices.SortStableFunc(qs, func(a, b erp.Order) int {
			return cmp.Or(cmp.Compare(rank(a), rank(b)), cmp.Compare(dueOf(a), dueOf(b)), a.Created.Compare(b.Created))
		})
		for _, o := range qs {
			ds = append(ds, stores.Demand{No: o.No, Part: o.PartNo, Product: o.Product, Qty: math.Max(0, float64(o.Qty-o.Good))})
		}
		if k, err := s.Stores.Kit(company, ds); err == nil {
			for _, x := range k.Orders {
				if x.Short == 0 {
					continue
				}
				var what []string
				for _, n := range x.Needs {
					if n.Short > 1e-9 && len(what) < 2 {
						what = append(what, fmt.Sprintf("%s 缺 %s %s", strings.TrimSpace(n.Name+" "+n.Spec), num(n.Short), n.Unit))
					}
				}
				if x.Short > len(what) {
					what = append(what, fmt.Sprintf("等 %d 样", x.Short))
				}
				short[x.No] = strings.Join(what, "，")
			}
		} else {
			s.Log.Warn("plan: kitting", "company", company, "err", err)
		}
		if ds, err := s.Stores.Dies(company); err == nil {
			pressLine := map[string]string{}
			for _, p := range m.Plants {
				for _, l := range p.Lines {
					for _, pr := range l.Presses {
						pressLine[p.ID+"/"+pr.ID] = p.ID + "/" + l.ID
					}
				}
			}
			for _, d := range ds {
				x := Die{No: d.No, Kind: d.Kind, Product: d.Product, Life: d.Life, Left: d.Left}
				switch d.Place {
				case stores.OnPress:
					x.Line = pressLine[d.Plant+"/"+d.Asset]
				case stores.InStore:
				default:
					x.Left = 0 // in repair, on trial or scrapped: not to plan with
				}
				in.Dies = append(in.Dies, x)
			}
		}
	}
	for _, o := range list {
		if o.Status != erp.Queued {
			continue
		}
		x := Order{No: o.No, Part: cmp.Or(o.PartNo, o.Product), Name: o.Name, Customer: o.Customer, Qty: max(1, o.Qty-o.Good), Created: o.Created}
		if o.Due != nil {
			x.Due = *o.Due
		}
		w := wants[o.No]
		for _, l := range in.Lines {
			pl := byKey[l.Key]
			p := plantOf[l.Key]
			if w.Plant != "" && w.Plant != p.ID && w.Plant != p.Name {
				continue
			}
			ok := false
			switch {
			case w.Line != "":
				ok = w.Line == pl.ID || w.Line == l.Key || w.Line == pl.Name
			default:
				ok = makes(pl, o.PartNo) || makes(pl, o.Product) || (o.PartNo == "" && o.Product == "" && l.Key == o.Plant+"/"+o.Line)
			}
			if ok {
				x.Lines = append(x.Lines, l.Key)
				x.Family = append(x.Family, pl.Product, pl.ProductName)
			}
		}
		// the line it is on now first
		if k := o.Plant + "/" + o.Line; slices.Contains(x.Lines, k) {
			x.Lines = append([]string{k}, slices.DeleteFunc(slices.Clone(x.Lines), func(v string) bool { return v == k })...)
		}
		if why := short[o.No]; why != "" {
			x.Short = why
			if at, ok := st.Arrive[o.No]; ok && !at.IsZero() {
				x.Release = at
			} else if st.Material == Hold {
				x.Hold = true
			}
		}
		if lk, ok := st.Locks[o.No]; ok {
			l := lk
			x.Lock = &l
		}
		in.Orders = append(in.Orders, x)
	}
	return in, nil
}

func rank(o erp.Order) int {
	if o.Status == erp.Running {
		return 0
	}
	return 1
}

func dueOf(o erp.Order) int64 {
	if o.Due == nil {
		return math.MaxInt64
	}
	return o.Due.UnixMilli()
}

func num(v float64) string {
	if v == math.Trunc(v) {
		return fmt.Sprintf("%.0f", v)
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.3f", v), "0"), ".")
}

// Change is an order a plan puts elsewhere than the plan it is compared with.
type Change struct {
	No       string `json:"no"`
	Part     string `json:"part"`
	From     string `json:"from"`
	FromName string `json:"fromName"`
	FromSeq  int    `json:"fromSeq"`
	To       string `json:"to"`
	ToName   string `json:"toName"`
	ToSeq    int    `json:"toSeq"`
}

// Diff is how a draft differs from the plan in force (or, before any is published, the order
// the lines would run the queue in): orders on another line, in another place on the same line,
// held back or let go, and the figures of both.
type Diff struct {
	Against   string   `json:"against"` // published | queue
	Line      []Change `json:"line"`
	Order     []Change `json:"order"`
	Held      []string `json:"held"`
	Released  []string `json:"released"`
	Count     int      `json:"count"`
	Text      string   `json:"text"`
	KPI       KPI      `json:"kpi"`
	BaseKPI   KPI      `json:"baseKpi"`
	BaseLabel string   `json:"baseLabel"`
}

func compare(base, next *Result, against string) *Diff {
	d := &Diff{Against: against, Line: []Change{}, Order: []Change{}, Held: []string{}, Released: []string{}, KPI: next.KPI, BaseKPI: base.KPI}
	d.BaseLabel = map[string]string{Published: "现在发布的计划", "queue": "现在的排队顺序"}[against]
	type at struct {
		line, name string
		seq        int
	}
	was := map[string]at{}
	wasHeld := map[string]bool{}
	for _, o := range base.Orders {
		if !o.Running {
			was[o.No] = at{o.Line, o.LineName, o.Seq}
		}
	}
	for _, o := range base.Held {
		wasHeld[o.No] = true
	}
	// a place counts by the orders both have on the line: one order more ahead is no change
	order := func(r *Result, line string) []string {
		var out []string
		for _, o := range r.Orders {
			if o.Line == line && !o.Running {
				out = append(out, o.No)
			}
		}
		return out
	}
	isHeld := map[string]bool{}
	for _, o := range next.Held {
		isHeld[o.No] = true
		if !wasHeld[o.No] {
			d.Held = append(d.Held, o.No)
		}
	}
	for no := range wasHeld {
		if !isHeld[no] {
			d.Released = append(d.Released, no)
		}
	}
	slices.Sort(d.Released)
	for _, o := range next.Orders {
		w, ok := was[o.No]
		if o.Running || !ok {
			continue
		}
		c := Change{No: o.No, Part: o.Part, From: w.line, FromName: w.name, FromSeq: w.seq, To: o.Line, ToName: o.LineName, ToSeq: o.Seq}
		if w.line != o.Line {
			d.Line = append(d.Line, c)
			continue
		}
		// the same line: is it after a different order of the ones both plans have there?
		a, b := order(base, o.Line), order(next, o.Line)
		common := func(xs, other []string) []string {
			return slices.DeleteFunc(slices.Clone(xs), func(x string) bool { return !slices.Contains(other, x) })
		}
		ca, cb := common(a, b), common(b, a)
		if slices.Index(ca, o.No) != slices.Index(cb, o.No) {
			d.Order = append(d.Order, c)
		}
	}
	d.Count = len(d.Line) + len(d.Order) + len(d.Held) + len(d.Released)
	var parts []string
	if n := len(d.Line); n > 0 {
		parts = append(parts, fmt.Sprintf("%d 张换了产线", n))
	}
	if n := len(d.Order); n > 0 {
		parts = append(parts, fmt.Sprintf("%d 张换了先后", n))
	}
	if n := len(d.Held); n > 0 {
		parts = append(parts, fmt.Sprintf("%d 张缺料先不排", n))
	}
	if n := len(d.Released); n > 0 {
		parts = append(parts, fmt.Sprintf("%d 张放出来排了", n))
	}
	if len(parts) == 0 {
		d.Text = "和" + d.BaseLabel + "一样"
	} else {
		d.Text = "和" + d.BaseLabel + "比：" + strings.Join(parts, "，")
	}
	return d
}

// Overview is a company's planning as the console shows it: the draft, the plan in force, the
// queue as it stands, how the draft differs, and what changed since the plan was published.
type Overview struct {
	Draft     *Plan `json:"draft"`
	Published *Plan `json:"published"`
	// Current is the plan in force (or the queue) laid out again as things stand now.
	Current *Result  `json:"current"`
	Diff    *Diff    `json:"diff"`
	Suggest []string `json:"suggest"`
	// Settings are how the company plans.
	Settings Settings   `json:"settings"`
	Goals    []GoalInfo `json:"goals"`
	WhatIf   *WhatIf    `json:"whatIf,omitempty"`
}

// GoalInfo is a goal and how it reads.
type GoalInfo struct {
	ID    Goal   `json:"id"`
	Label string `json:"label"`
	Note  string `json:"note"`
}

var goals = []GoalInfo{
	{Due, "交期优先", "尽量都按交期交，多换几次型也行"},
	{Balance, "平衡", "交期和换型都顾"},
	{Setups, "少换型", "同料号尽量连着做，个别单子可能晚一点"},
}

// base is the order of work the draft is compared with: the plan in force, or the queue.
func (s *Service) base(company string, pub *Plan) (Seq, string, error) {
	if pub != nil {
		return pub.Result.Seq(), Published, nil
	}
	q, err := s.Orders.Store.Queued(company)
	return Seq(q), "queue", err
}

// Overview is the company's planning now.
func (s *Service) Overview(company string) (*Overview, error) {
	st, err := s.Store.settings(company)
	if err != nil {
		return nil, err
	}
	draft, err := s.Store.get(company, Draft)
	if err != nil {
		return nil, err
	}
	pub, err := s.Store.get(company, Published)
	if err != nil {
		return nil, err
	}
	in, err := s.input(company, st, st.Goal)
	if err != nil {
		return nil, err
	}
	seq, against, err := s.base(company, pub)
	if err != nil {
		return nil, err
	}
	cur := Evaluate(in, seq)
	ov := &Overview{Draft: draft, Published: pub, Current: cur, Settings: st, Goals: goals, Suggest: s.suggest(in, pub, draft)}
	if draft != nil {
		ov.Diff = compare(cur, draft.Result, against)
	}
	s.mu.Lock()
	if j := s.runs[company]; j != nil {
		w := j.status()
		ov.WhatIf = &w
	}
	s.mu.Unlock()
	return ov, nil
}

// suggest says what changed since the plan in force was made, as a reason to plan again: orders
// added or withdrawn, a line stopped, material that came.
func (s *Service) suggest(in *Input, pub, draft *Plan) []string {
	var out []string
	ref := pub
	if draft != nil && (pub == nil || draft.Created.After(pub.Created)) {
		ref = draft
	}
	if ref == nil {
		if len(in.Orders) > 0 {
			out = append(out, fmt.Sprintf("还没有排过计划：%d 张工单按交期先后在排队，点「重新排」排一份", len(in.Orders)))
		}
		return out
	}
	in0 := map[string]Planned{}
	for _, o := range ref.Result.Orders {
		in0[o.No] = o
	}
	for _, o := range ref.Result.Held {
		in0[o.No] = o
	}
	var added, changed []string
	open := map[string]bool{}
	for _, o := range in.Orders {
		open[o.No] = true
		p, ok := in0[o.No]
		switch {
		case !ok:
			added = append(added, o.No)
		case p.Due.Unix() != o.Due.Unix() && !p.Running:
			changed = append(changed, o.No)
		case p.Held && !o.Hold:
			changed = append(changed, o.No)
		}
	}
	running := map[string]bool{}
	for _, l := range in.Lines {
		if l.Running != nil {
			running[l.Running.No] = true
		}
	}
	var gone []string
	for no, p := range in0 {
		if !open[no] && !running[no] && !p.Running && p.Start.After(in.Now) {
			gone = append(gone, no)
		}
	}
	slices.Sort(gone)
	what := map[bool]string{true: "草稿", false: "发布的计划"}[ref == draft]
	if len(added) > 0 {
		out = append(out, fmt.Sprintf("排完%s后新来了 %d 张工单（%s）", what, len(added), strings.Join(clip(added, 4), "、")))
	}
	if len(gone) > 0 {
		out = append(out, fmt.Sprintf("%d 张工单撤回或提前做了（%s）", len(gone), strings.Join(clip(gone, 4), "、")))
	}
	if len(changed) > 0 {
		out = append(out, fmt.Sprintf("%d 张工单改了交期或到了料（%s）", len(changed), strings.Join(clip(changed, 4), "、")))
	}
	for _, l := range in.Lines {
		if l.Down != "" && slices.ContainsFunc(ref.Result.Orders, func(o Planned) bool { return o.Line == l.Key && !o.Running }) {
			out = append(out, fmt.Sprintf("%s停着（%s），排在它上面的工单可能要挪", l.Name, l.Down))
		}
	}
	if len(out) > 0 {
		out = append(out, "建议「重新排」看看，满意了再发布")
	}
	return out
}

func clip(xs []string, n int) []string {
	if len(xs) <= n {
		return xs
	}
	return append(slices.Clone(xs[:n]), fmt.Sprintf("等 %d 张", len(xs)))
}

// Replan makes a new draft for the goal (the settings' when empty), the locked orders kept.
func (s *Service) Replan(company string, goal Goal, by string) (*Plan, error) {
	st, err := s.Store.settings(company)
	if err != nil {
		return nil, err
	}
	if goal != "" {
		if GoalLabel[goal] == "" {
			return nil, refuse("目标只能是 due、balance 或 setup")
		}
		st.Goal = goal
		if err := s.Store.saveSettings(company, st); err != nil {
			return nil, err
		}
	}
	in, err := s.input(company, st, st.Goal)
	if err != nil {
		return nil, err
	}
	r := Schedule(in)
	p := &Plan{ID: newID(), Status: Draft, Goal: st.Goal, Created: time.Now(), By: by, Note: "按「" + GoalLabel[st.Goal] + "」重新排", Result: r}
	return p, s.Store.saveDraft(company, p)
}

// working is the draft to change: the draft, else a copy of the plan in force, else the queue.
func (s *Service) working(company string, st Settings) (*Input, Seq, error) {
	in, err := s.input(company, st, st.Goal)
	if err != nil {
		return nil, nil, err
	}
	draft, err := s.Store.get(company, Draft)
	if err != nil {
		return nil, nil, err
	}
	if draft != nil {
		return in, draft.Result.Seq(), nil
	}
	pub, err := s.Store.get(company, Published)
	if err != nil {
		return nil, nil, err
	}
	seq, _, err := s.base(company, pub)
	return in, seq, err
}

// Move puts an order on a line at a place (0 first) in the draft, as it then lays out.
func (s *Service) Move(company, no, line string, at int, by string) (*Plan, error) {
	st, err := s.Store.settings(company)
	if err != nil {
		return nil, err
	}
	if _, ok := st.Locks[no]; ok {
		return nil, refuse("工单 %s 锁定了，先解锁再挪", no)
	}
	in, seq, err := s.working(company, st)
	if err != nil {
		return nil, err
	}
	r, err := Move(in, seq, no, line, at)
	if err != nil {
		return nil, &Problems{List: []string{err.Error()}}
	}
	name := line
	for _, l := range in.Lines {
		if l.Key == line {
			name = l.Name
		}
	}
	p := &Plan{ID: newID(), Status: Draft, Goal: st.Goal, Created: time.Now(), By: by, Note: fmt.Sprintf("把 %s 挪到%s第 %d 位", no, name, at+1), Result: r}
	return p, s.Store.saveDraft(company, p)
}

// Lock keeps an order where the draft has it (on false, lets it go again), and lays the draft out again.
func (s *Service) Lock(company, no string, on bool, by string) (*Plan, error) {
	st, err := s.Store.settings(company)
	if err != nil {
		return nil, err
	}
	in, seq, err := s.working(company, st)
	if err != nil {
		return nil, err
	}
	cur := Evaluate(in, seq)
	if on {
		i := slices.IndexFunc(cur.Orders, func(o Planned) bool { return o.No == no })
		if i < 0 || cur.Orders[i].Running {
			return nil, refuse("计划里没有排着的工单 %s（在做的不用锁）", no)
		}
		o := cur.Orders[i]
		st.Locks[no] = Lock{Line: o.Line, Start: o.SetupStart}
	} else {
		delete(st.Locks, no)
	}
	if err := s.Store.saveSettings(company, st); err != nil {
		return nil, err
	}
	in, err = s.input(company, st, st.Goal)
	if err != nil {
		return nil, err
	}
	note := "锁定 " + no
	if !on {
		note = "解锁 " + no
	}
	p := &Plan{ID: newID(), Status: Draft, Goal: st.Goal, Created: time.Now(), By: by, Note: note, Result: Evaluate(in, seq)}
	return p, s.Store.saveDraft(company, p)
}

// SetSettings changes how the company plans: goal, material, expected arrivals, planned stops.
// Locks are changed one by one with Lock.
func (s *Service) SetSettings(company string, in Settings) (Settings, error) {
	st, err := s.Store.settings(company)
	if err != nil {
		return st, err
	}
	in.Locks = st.Locks
	in.normalize()
	var problems []string
	for i, x := range in.Stops {
		if x.To <= x.From {
			problems = append(problems, fmt.Sprintf("第 %d 个停线计划的结束时间要晚于开始", i+1))
		}
	}
	if len(problems) > 0 {
		return st, &Problems{List: problems}
	}
	return in, s.Store.saveSettings(company, in)
}

// Discard throws the draft away.
func (s *Service) Discard(company string) error { return s.Store.discard(company) }

// Published is what publishing a draft did.
type PublishResult struct {
	Plan    *Plan `json:"plan"`
	Diff    *Diff `json:"diff"`
	Changed int   `json:"changed"`
}

// Publish makes the draft the plan in force: each queued order gets its line and place in the
// queue the lines run, and those it holds back wait.
func (s *Service) Publish(company, by string) (*PublishResult, error) {
	draft, err := s.Store.get(company, Draft)
	if err != nil {
		return nil, err
	}
	if draft == nil {
		return nil, refuse("还没有草稿：先「重新排」一份")
	}
	pub, err := s.Store.get(company, Published)
	if err != nil {
		return nil, err
	}
	st, err := s.Store.settings(company)
	if err != nil {
		return nil, err
	}
	in, err := s.input(company, st, draft.Goal)
	if err != nil {
		return nil, err
	}
	seq, against, err := s.base(company, pub)
	if err != nil {
		return nil, err
	}
	diff := compare(Evaluate(in, seq), draft.Result, against)
	byKey := map[string]Line{}
	for _, l := range in.Lines {
		byKey[l.Key] = l
	}
	var as []erp.Assign
	for _, o := range draft.Result.Orders {
		if o.Running {
			continue
		}
		l := byKey[o.Line]
		as = append(as, erp.Assign{No: o.No, Plant: l.Plant, PlantName: l.PlantName, Line: l.ID, LineName: l.Name, Seq: o.Seq})
	}
	var hold []string
	for _, o := range draft.Result.Held {
		hold = append(hold, o.No)
	}
	n, err := s.Orders.ApplyPlan(company, as, hold)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if err := s.Store.publish(company, draft.ID, now); err != nil {
		return nil, err
	}
	draft.Status, draft.Published = Published, &now
	return &PublishResult{Plan: draft, Diff: diff, Changed: n}, nil
}

// Plans are the company's plans in force and retired, newest first (without their orders).
func (s *Service) History(company string) ([]Plan, error) {
	rows, err := s.Store.db.Query(`SELECT id, status, goal, created, by, published, note, body FROM plans WHERE company=? AND status<>? ORDER BY created DESC LIMIT 20`, company, Draft)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Plan{}
	for rows.Next() {
		var p Plan
		var created, published int64
		var body string
		if err := rows.Scan(&p.ID, &p.Status, &p.Goal, &created, &p.By, &published, &p.Note, &body); err != nil {
			return nil, err
		}
		p.Created = time.UnixMilli(created)
		if published > 0 {
			t := time.UnixMilli(published)
			p.Published = &t
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ErrNoOrders is said when there is nothing to plan.
var ErrNoOrders = errors.New("没有排队的工单")
