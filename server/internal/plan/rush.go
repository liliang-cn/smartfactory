package plan

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
)

// Insert puts an order (one of in.Orders, not in s) into an order of work where it costs least,
// the others staying in their order: each line that can make it, at the front, at its due-date
// place, at the end and (on a short line) everywhere between.
func Insert(in *Input, s Seq, no string) (*Result, error) {
	p := newProblem(in)
	i := slices.IndexFunc(p.ords, func(o *Order) bool { return o.No == no })
	if i < 0 {
		return nil, fmt.Errorf("没有工单 %s", no)
	}
	if len(p.elig[i]) == 0 {
		return nil, fmt.Errorf("没有产线能做 %s", p.ords[i].Part)
	}
	seq := p.fromSeq(s)
	for j := range seq {
		seq[j] = slices.DeleteFunc(seq[j], func(x int) bool { return x == i })
	}
	var best [][]int
	bestCost := math.Inf(1)
	for _, j := range p.elig[i] {
		at := []int{0, len(seq[j])}
		if d := p.due[i]; d > 0 {
			k := 0
			for k < len(seq[j]) && p.due[seq[j][k]] != 0 && p.due[seq[j][k]] <= d {
				k++
			}
			at = append(at, k)
		}
		if len(seq[j]) <= 30 {
			for k := 1; k < len(seq[j]); k++ {
				at = append(at, k)
			}
		}
		slices.Sort(at)
		for _, k := range slices.Compact(at) {
			next := clone(seq)
			next[j] = slices.Insert(next[j], k, i)
			if c := p.decode(next, false).cost; c < bestCost-1e-9 {
				best, bestCost = next, c
			}
		}
	}
	return p.result(best, nil), nil
}

// Impact is what an order added to the plan does: where it goes, when it would be done, and
// the orders it pushes back.
type Impact struct {
	Order    Planned   `json:"order"`
	OnTime   bool      `json:"onTime"`
	Earliest time.Time `json:"earliest"`
	// Pushed are the orders that end later because of it; NewlyLate those it makes late.
	Pushed    []Push `json:"pushed"`
	NewlyLate int    `json:"newlyLate"`
	Before    KPI    `json:"before"`
	After     KPI    `json:"after"`
	Against   string `json:"against"`
	Note      string `json:"note"`
}

// Push is an order pushed back by another.
type Push struct {
	No       string    `json:"no"`
	Customer string    `json:"customer,omitempty"`
	Part     string    `json:"part"`
	Line     string    `json:"line"`
	Was      time.Time `json:"was"`
	Now      time.Time `json:"now"`
	DelayMin float64   `json:"delayMin"`
	Due      time.Time `json:"due,omitzero"`
	Late     bool      `json:"late"`
	WasLate  bool      `json:"wasLate"`
}

// RushInput is a hypothetical order: what, how many, by when.
type RushInput struct {
	Part     string    `json:"part"`
	Qty      int       `json:"qty"`
	Due      time.Time `json:"due"`
	Customer string    `json:"customer,omitempty"`
}

// Rush works out what an urgent order would do to the plan in force (or the queue): placed where
// it costs least on 交期优先, the others keeping their order. Nothing is stored.
func (s *Service) Rush(company string, r RushInput) (*Impact, error) {
	if r.Qty <= 0 || strings.TrimSpace(r.Part) == "" {
		return nil, refuse("要说做什么（料号或产品）和多少件")
	}
	st, err := s.Store.settings(company)
	if err != nil {
		return nil, err
	}
	in, err := s.input(company, st, Due)
	if err != nil {
		return nil, err
	}
	pub, err := s.Store.get(company, Published)
	if err != nil {
		return nil, err
	}
	seq, against, err := s.base(company, pub)
	if err != nil {
		return nil, err
	}
	m := s.Eng.PlanModel(company)
	o := Order{No: "急单", Part: r.Part, Qty: r.Qty, Due: r.Due, Customer: r.Customer, Created: in.Now}
	for _, p := range m.Plants {
		for _, l := range p.Lines {
			if makes(l, r.Part) {
				o.Lines = append(o.Lines, p.ID+"/"+l.ID)
				o.Family = append(o.Family, l.Product, l.ProductName)
				for _, v := range l.Parts {
					if norm(v.PartNo) == norm(r.Part) || slices.Contains(names(v.Name), norm(r.Part)) {
						o.Part = v.PartNo
					}
				}
			}
		}
	}
	if len(o.Lines) == 0 {
		var known []string
		for _, p := range m.Plants {
			for _, l := range p.Lines {
				for _, v := range l.Parts {
					if !slices.Contains(known, v.PartNo) {
						known = append(known, v.PartNo)
					}
				}
			}
		}
		return nil, refuse("没有产线做「%s」。能做的料号有：%s", r.Part, strings.Join(known, "、"))
	}
	before := Evaluate(in, seq)
	in.Orders = append(in.Orders, o)
	after, err := Insert(in, seq, o.No)
	if err != nil {
		return nil, &Problems{List: []string{err.Error()}}
	}
	out := &Impact{Before: before.KPI, After: after.KPI, Against: map[string]string{Published: "发布的计划", "queue": "现在的排队顺序"}[against], Pushed: []Push{}}
	was := map[string]Planned{}
	for _, x := range before.Orders {
		was[x.No] = x
	}
	for _, x := range after.Orders {
		if x.No == o.No {
			out.Order = x
			out.OnTime = !x.Late
			continue
		}
		w, ok := was[x.No]
		if !ok || x.Running {
			continue
		}
		if d := x.End.Sub(w.End).Minutes(); d >= 10 {
			out.Pushed = append(out.Pushed, Push{No: x.No, Customer: x.Customer, Part: x.Part, Line: x.LineName, Was: w.End, Now: x.End, DelayMin: math.Round(d), Due: x.Due, Late: x.Late, WasLate: w.Late})
			if x.Late && !w.Late {
				out.NewlyLate++
			}
		}
	}
	slices.SortFunc(out.Pushed, func(a, b Push) int { return cmp.Compare(b.DelayMin, a.DelayMin) })
	// how soon it could be done if it went first on its best line
	p := newProblem(&Input{Now: in.Now, Lines: in.Lines, Orders: []Order{o}, Stops: in.Stops, Goal: Due})
	if e := p.decode(p.construct(), false); e.line[0] >= 0 {
		out.Earliest = time.Unix(e.end[0], 0)
	}
	when := out.Order.End.In(cst).Format("01-02 15:04")
	if out.OnTime {
		out.Note = fmt.Sprintf("排在%s第 %d 位，%s 做完，赶得上", out.Order.LineName, out.Order.Seq, when)
	} else {
		out.Note = fmt.Sprintf("排在%s第 %d 位，%s 才做完，晚 %s", out.Order.LineName, out.Order.Seq, when, hours(int64(out.Order.LateMin*60)))
	}
	if len(out.Pushed) == 0 {
		out.Note += "；别的工单都不受影响"
	} else {
		out.Note += fmt.Sprintf("；%d 张工单往后推", len(out.Pushed))
		if out.NewlyLate > 0 {
			out.Note += fmt.Sprintf("，其中 %d 张会因此误交期", out.NewlyLate)
		}
	}
	return out, nil
}

// Status is the plan in force in brief, for the assistant: the figures, the late orders and why,
// what to watch, and what changed since it was made.
func (s *Service) Status(company string) (map[string]any, error) {
	ov, err := s.Overview(company)
	if err != nil {
		return nil, err
	}
	r, what := ov.Current, "现在的排队顺序（还没有发布过计划）"
	if ov.Published != nil {
		what = fmt.Sprintf("发布的计划（%s 发布，%s）", ov.Published.Published.In(cst).Format("01-02 15:04"), GoalLabel[ov.Published.Goal])
	}
	day := func(t time.Time) string {
		if t.IsZero() {
			return ""
		}
		return t.In(cst).Format("01-02 15:04")
	}
	order := func(o Planned) map[string]any {
		m := map[string]any{"no": o.No, "part": strings.TrimSpace(o.Part + " " + o.Name), "qty": o.Qty, "line": o.LineName, "start": day(o.Start), "end": day(o.End), "due": day(o.Due)}
		if o.Customer != "" {
			m["customer"] = o.Customer
		}
		if o.Late {
			m["lateHours"] = math.Round(o.LateMin/6) / 10
			var why []string
			for _, w := range o.Why {
				why = append(why, w.Text)
			}
			m["why"] = why
		}
		var flags []string
		for _, f := range o.Flags {
			flags = append(flags, f.Text)
		}
		if len(flags) > 0 {
			m["flags"] = flags
		}
		if o.Running {
			m["running"] = true
		}
		if o.Locked {
			m["locked"] = true
		}
		return m
	}
	var late, watch, all []map[string]any
	for _, o := range r.Orders {
		all = append(all, order(o))
		if o.Late {
			late = append(late, order(o))
		} else if len(o.Flags) > 0 {
			watch = append(watch, order(o))
		}
	}
	var held []map[string]any
	for _, o := range r.Held {
		held = append(held, order(o))
	}
	lines := []map[string]any{}
	for _, l := range r.Lines {
		if l.Orders == 0 {
			continue
		}
		lines = append(lines, map[string]any{"line": l.Name, "orders": l.Orders, "util": math.Round(l.Util * 100), "busyUntil": day(l.BusyUntil), "rate": l.RateSource, "changeover": fmt.Sprintf("%.0f 分钟（%s）", l.Setup, l.SetupSrc)})
	}
	k := r.KPI
	out := map[string]any{
		"plan":  what,
		"kpi":   fmt.Sprintf("排了 %d 张，按期 %d 张（%.1f%%），误期 %d 张共 %.1f 小时，换型 %d 次共 %.1f 小时，全部做完 %s；缺料先不排 %d 张", k.Planned, k.OnTime, k.OnTimePct, k.Late, k.LateHours, k.Setups, k.SetupHours, day(k.End), k.Held),
		"lines": lines, "late": late, "watch": watch, "held": held, "orders": all,
	}
	if ov.Draft != nil {
		out["draft"] = fmt.Sprintf("另有一份没发布的草稿（%s，%s）：%s", ov.Draft.Note, ov.Draft.By, ov.Diff.Text)
	}
	if len(ov.Suggest) > 0 {
		out["changedSince"] = ov.Suggest
	}
	return out, nil
}
