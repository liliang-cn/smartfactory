package plan

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"smartfactory/internal/engine"
)

// A what-if check runs a plan on a copy of the plant (the twin's simulation, nobody watching) and
// sets when each order finished there beside when the plan said it would: how realistic the plan
// is. It runs in the background, one at a time, for at most a week of plant time and a few
// minutes of the server's.

// WhatIf is how a check stands, and what it found.
type WhatIf struct {
	Status  string    `json:"status"` // running | done | failed
	Plan    string    `json:"plan"`   // draft | published
	PlanID  string    `json:"planId"`
	Days    float64   `json:"days"`
	Started time.Time `json:"started"`
	Took    float64   `json:"took,omitempty"`
	Error   string    `json:"error,omitempty"`
	Result  *Check    `json:"result,omitempty"`
}

// Check is a plan held against its run.
type Check struct {
	Hours  float64    `json:"hours"`
	Orders []CheckRow `json:"orders"`
	// Finished is how many of the plan's orders the run finished, of Due those the plan has done
	// within the run; MeanDev and MaxDev the mean and largest difference in hours (run − plan).
	Finished int     `json:"finished"`
	Due      int     `json:"due"`
	MeanDev  float64 `json:"meanDev"`
	MaxDev   float64 `json:"maxDev"`
	// PlanOnTime and RunOnTime are on-time orders by the plan and by the run, among those with a due date the run finished.
	PlanOnTime int    `json:"planOnTime"`
	RunOnTime  int    `json:"runOnTime"`
	WithDue    int    `json:"withDue"`
	Verdict    string `json:"verdict"`
}

// CheckRow is one order: planned and simulated start and end.
type CheckRow struct {
	No        string    `json:"no"`
	Line      string    `json:"line"`
	PlanStart time.Time `json:"planStart"`
	PlanEnd   time.Time `json:"planEnd"`
	RunStart  time.Time `json:"runStart,omitzero"`
	RunEnd    time.Time `json:"runEnd,omitzero"`
	DevHours  *float64  `json:"devHours,omitempty"`
	Due       time.Time `json:"due,omitzero"`
	// Left: the run ended with it unfinished (good of qty made).
	Good int  `json:"good,omitempty"`
	Qty  int  `json:"qty,omitempty"`
	Open bool `json:"open,omitempty"`
}

type whatIfJob struct {
	mu  sync.Mutex
	w   WhatIf
	end chan struct{}
}

func (j *whatIfJob) status() WhatIf {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.w
}

// one check runs at a time: they are CPU-bound
var slot = make(chan struct{}, 1)

// StartWhatIf starts checking the draft or the published plan over days of plant time (½–7).
func (s *Service) StartWhatIf(company, which string, days float64) (WhatIf, error) {
	if which != Published {
		which = Draft
	}
	p, err := s.Store.get(company, which)
	if err != nil {
		return WhatIf{}, err
	}
	if p == nil {
		return WhatIf{}, refuse("%s", map[string]string{Draft: "没有草稿可以推演", Published: "还没有发布的计划"}[which])
	}
	days = math.Min(math.Max(days, 0.5), 7)
	s.mu.Lock()
	if j := s.runs[company]; j != nil && j.status().Status == "running" {
		s.mu.Unlock()
		return j.status(), nil
	}
	j := &whatIfJob{w: WhatIf{Status: "running", Plan: which, PlanID: p.ID, Days: days, Started: time.Now()}, end: make(chan struct{})}
	s.runs[company] = j
	s.mu.Unlock()
	state := s.Eng.SaveJSON(company)
	// the plan's order timed from now, as the copy stands: an order a line has taken since the
	// plan was made is running there, not waiting to be fed again
	r := p.Result
	if st, err := s.Store.settings(company); err == nil {
		if in, err := s.input(company, st, p.Goal); err == nil {
			r = Evaluate(in, p.Result.Seq())
		}
	}
	go func() {
		defer close(j.end)
		res, err := s.check(company, state, r, days)
		j.mu.Lock()
		defer j.mu.Unlock()
		j.w.Took = math.Round(time.Since(j.w.Started).Seconds())
		if err != nil {
			j.w.Status, j.w.Error = "failed", err.Error()
			s.Log.Warn("plan: what-if", "company", company, "err", err)
			return
		}
		j.w.Status, j.w.Result = "done", res
	}()
	return j.status(), nil
}

// WhatIfStatus is the last check of the company, nil when there was none.
func (s *Service) WhatIfStatus(company string) *WhatIf {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j := s.runs[company]; j != nil {
		w := j.status()
		return &w
	}
	return nil
}

// Wait waits up to d for the company's check to end (tests, the assistant).
func (s *Service) Wait(company string, d time.Duration) *WhatIf {
	s.mu.Lock()
	j := s.runs[company]
	s.mu.Unlock()
	if j == nil {
		return nil
	}
	select {
	case <-j.end:
	case <-time.After(d):
	}
	w := j.status()
	return &w
}

func (s *Service) check(company, state string, r *Result, days float64) (*Check, error) {
	slot <- struct{}{}
	defer func() { <-slot }()
	queue := map[string][]engine.ExtOrder{}
	var last time.Time
	for _, o := range r.Orders {
		if o.End.After(last) {
			last = o.End
		}
		if o.Running {
			continue
		}
		x := engine.ExtOrder{No: o.No, PartNo: o.Part, Name: o.Name, Qty: o.Qty, Customer: o.Customer}
		if !o.Due.IsZero() {
			x.Due = o.Due.UnixMilli()
		}
		queue[o.Line] = append(queue[o.Line], x)
	}
	// as far as the plan goes and a bit, within the days asked
	hours := math.Min(days*24, math.Max(4, last.Sub(r.Now).Hours()+8))
	run, err := s.run(company, state, queue, hours)
	if err != nil {
		return nil, err
	}
	return compareRun(r, run, time.UnixMilli(run.Start)), nil
}

func (s *Service) run(company, state string, queue map[string][]engine.ExtOrder, hours float64) (*engine.PlanRunResult, error) {
	const dt = 2
	if node, err := exec.LookPath("node"); err == nil {
		return runInNode(node, s.Eng, company, state, queue, hours, dt)
	}
	off, err := engine.NewOffline(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))
	if err != nil {
		return nil, err
	}
	off.Dir = s.Eng.Dir
	off.LoadConfigs()
	var rs struct{ Restored, Skipped []string }
	if err := json.Unmarshal([]byte(off.RestoreJSON(company, state)), &rs); err != nil || len(rs.Restored) == 0 {
		return nil, fmt.Errorf("复制不了工厂的状态（%v）", rs.Skipped)
	}
	return off.PlanRun(company, queue, hours, dt)
}

// compareRun sets each planned order's end beside its end in the run (relative to their own
// starts: the run starts later than the plan was made, so times are shifted by that).
func compareRun(r *Result, run *engine.PlanRunResult, runStart time.Time) *Check {
	shift := runStart.Sub(r.Now)
	c := &Check{Hours: math.Round(run.Hours*10) / 10, Orders: []CheckRow{}}
	start, end := map[string]time.Time{}, map[string]time.Time{}
	for _, e := range run.Events {
		switch e.Kind {
		case "start", "running":
			start[e.No] = time.UnixMilli(e.At)
		case "done":
			end[e.No] = time.UnixMilli(e.At)
		}
	}
	left := map[string][2]int{}
	for _, l := range run.Left {
		left[l.No] = [2]int{l.Good, l.Qty}
	}
	runEnd := time.UnixMilli(run.End)
	var sum float64
	for _, o := range r.Orders {
		row := CheckRow{No: o.No, Line: o.LineName, PlanStart: o.Start.Add(shift), PlanEnd: o.End.Add(shift), Due: o.Due, RunStart: start[o.No], RunEnd: end[o.No]}
		if o.Running {
			row.PlanStart = runStart
		}
		if !row.RunEnd.IsZero() {
			d := math.Round(row.RunEnd.Sub(row.PlanEnd).Hours()*10) / 10
			row.DevHours = &d
			c.Finished++
			sum += math.Abs(d)
			c.MaxDev = math.Max(c.MaxDev, math.Abs(d))
			if !o.Due.IsZero() {
				c.WithDue++
				if !o.Late {
					c.PlanOnTime++
				}
				if !row.RunEnd.After(o.Due) {
					c.RunOnTime++
				}
			}
		} else if g, ok := left[o.No]; ok {
			row.Good, row.Qty, row.Open = g[0], g[1], true
		}
		if !row.PlanEnd.After(runEnd) {
			c.Due++
		}
		c.Orders = append(c.Orders, row)
	}
	slices.SortStableFunc(c.Orders, func(a, b CheckRow) int { return a.PlanEnd.Compare(b.PlanEnd) })
	if c.Finished > 0 {
		c.MeanDev = math.Round(sum/float64(c.Finished)*10) / 10
	}
	switch {
	case c.Due == 0:
		c.Verdict = "推演时间太短，计划里还没有单子该做完"
	case c.Finished < c.Due*8/10:
		c.Verdict = fmt.Sprintf("计划偏乐观：按计划这段时间该做完 %d 张，仿真只做完 %d 张", c.Due, c.Finished)
	case c.MeanDev <= 1:
		c.Verdict = fmt.Sprintf("计划靠谱：做完的 %d 张，完工时间和计划平均差 %.1f 小时", c.Finished, c.MeanDev)
	case c.MeanDev <= 4:
		c.Verdict = fmt.Sprintf("计划大体可信：平均差 %.1f 小时，最多差 %.1f 小时", c.MeanDev, c.MaxDev)
	default:
		c.Verdict = fmt.Sprintf("计划和仿真差得多：平均差 %.1f 小时，最多 %.1f 小时，节拍或效率可能要校准", c.MeanDev, c.MaxDev)
	}
	return c
}

// runner loads the bundle, the company's run-time configs (if it was created at run time) and the
// live state, then runs the plan; input on stdin, result on stdout.
const runner = `const fs = require('fs');
globalThis.host = { log: (m) => process.stderr.write(String(m) + '\n') };
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const inp = JSON.parse(fs.readFileSync(0, 'utf8'));
const SF = globalThis.SF;
SF.init();
for (const ind of inp.industries) SF.addIndustry(JSON.stringify(ind));
if (inp.companyConfig) SF.addCompany(JSON.stringify(inp.companyConfig));
const r = JSON.parse(SF.restore(inp.company, inp.state));
if (!r.restored.length) { process.stdout.write(JSON.stringify({ ok: false, errors: ['复制不了工厂的状态：' + r.skipped.join(', ')] })); process.exit(0); }
process.stdout.write(SF.planRun(inp.company, JSON.stringify(inp.queue), inp.hours, inp.dt));
`

var bundle struct {
	sync.Once
	dir string
	err error
}

func runInNode(node string, eng *engine.Engine, company, state string, queue map[string][]engine.ExtOrder, hours, dt float64) (*engine.PlanRunResult, error) {
	bundle.Do(func() {
		b, err := engine.Bundle()
		if err == nil {
			bundle.dir, err = os.MkdirTemp("", "sf-plan-")
		}
		if err == nil {
			err = os.WriteFile(filepath.Join(bundle.dir, "engine.js"), b, 0o644)
		}
		if err == nil {
			err = os.WriteFile(filepath.Join(bundle.dir, "run.cjs"), []byte(runner), 0o644)
		}
		bundle.err = err
	})
	if bundle.err != nil {
		return nil, bundle.err
	}
	input := map[string]any{"company": company, "state": state, "queue": queue, "hours": hours, "dt": dt, "industries": []json.RawMessage{}}
	if c, inds, ok := eng.RuntimeCompany(company); ok {
		list := []json.RawMessage{}
		for _, v := range inds {
			list = append(list, v)
		}
		input["industries"], input["companyConfig"] = list, c
	}
	body, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, filepath.Join(bundle.dir, "run.cjs"), filepath.Join(bundle.dir, "engine.js"))
	cmd.Stdin = bytes.NewReader(body)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("推演没跑完：%v %s", err, strings.TrimSpace(tail(stderr.String(), 300)))
	}
	return engine.ParsePlanRun(string(out))
}

func tail(s string, n int) string {
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}
