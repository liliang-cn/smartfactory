package plan

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"smartfactory/internal/engine"
)

// A line does not make the bottleneck's cycle times the hour's performance figure. Over days it
// loses time to faults on any of its machines, tool changes when a die wears out, machines
// waiting for each other, the night's extra stops — none of which the last hour shows (a new
// twin has no hour behind it at all). So the rate a plan uses is measured the way the what-if is:
// each kind of line is run by itself on a copy of the plant for a few days of plant time, from
// the state it is in now, and the pieces it made counted against the working time its calendar
// gives it. The plan then lays orders out at that rate over the same calendar.

const (
	probeDays  = 3.0
	probeStale = 24 * time.Hour
	// a line whose part takes longer than this cannot be measured in a few days
	probeMaxCycle = 1200
	probeRetry    = 10 * time.Minute
)

// Probed is what the probe found: per line kind the efficiency (good pieces at the bottleneck's
// cycle over the working time) and how it was measured.
type Probed struct {
	// At is the plant clock the copy started from, Hours how long it ran.
	At    time.Time
	Hours float64
	Eff   map[string]float64
	// Runs is how many probes the figures average (a probe is a few days of a random run: each
	// new one is folded in, the last few counting alike).
	Runs int
	// sig is what the copy was made of (lines, cycles, tools): a change asks for a new probe.
	sig string
}

type probeState struct {
	mu      sync.Mutex
	got     *Probed
	running chan struct{}
	failed  time.Time
	err     error
}

// kind is what makes two lines alike for the probe: same plant and product, same machines.
func kind(plant string, l engine.PlanLine) string {
	return fmt.Sprintf("%s|%s|%.1f|%s", plant, l.Product, l.Neck, strings.Join(l.Tools, ","))
}

func modelSig(m *engine.PlanModel) string {
	var b strings.Builder
	for _, p := range m.Plants {
		for _, l := range p.Lines {
			b.WriteString(p.ID + "/" + l.ID + ":" + kind(p.ID, l) + ";")
		}
	}
	return b.String()
}

func (s *Service) probeOf(company string) *probeState {
	s.mu.Lock()
	defer s.mu.Unlock()
	ps := s.probes[company]
	if ps == nil {
		ps = &probeState{}
		s.probes[company] = ps
	}
	return ps
}

// Probed is the rates measured for the company, nil before the first probe is done. It starts a
// probe when there is none, the lines changed or the last is a day old (plant time); the old
// figures serve until the new ones are in.
func (s *Service) probed(company string, m *engine.PlanModel) *Probed {
	if s.NoProbe {
		return nil
	}
	ps := s.probeOf(company)
	ps.mu.Lock()
	defer ps.mu.Unlock()
	now := time.UnixMilli(m.Now)
	sig := modelSig(m)
	stale := ps.got == nil || ps.got.sig != sig || now.Sub(ps.got.At) > probeStale || now.Before(ps.got.At)
	if stale && ps.running == nil && time.Since(ps.failed) > probeRetry {
		done := make(chan struct{})
		ps.running = done
		go func() {
			p, err := s.probe(company, m, sig, ps.got)
			ps.mu.Lock()
			defer ps.mu.Unlock()
			ps.running = nil
			if err != nil {
				ps.failed, ps.err = time.Now(), err
				s.Log.Warn("plan: rate probe", "company", company, "err", err)
			} else {
				ps.got, ps.err = p, nil
			}
			close(done)
		}()
	}
	if ps.got != nil && ps.got.sig == sig {
		return ps.got
	}
	// the lines changed since: the old figures fit the kinds that are still there
	return ps.got
}

// WaitProbe waits up to d for a probe that is running (the first plan of a new twin).
func (s *Service) WaitProbe(company string, d time.Duration) {
	s.mu.Lock()
	ps := s.probes[company]
	s.mu.Unlock()
	if ps == nil {
		return
	}
	ps.mu.Lock()
	ch := ps.running
	ps.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case <-ch:
	case <-time.After(d):
	}
}

// Probing says a probe is running.
func (s *Service) Probing(company string) bool {
	s.mu.Lock()
	ps := s.probes[company]
	s.mu.Unlock()
	if ps == nil {
		return false
	}
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.running != nil
}

func (s *Service) probe(company string, m *engine.PlanModel, sig string, prev *Probed) (*Probed, error) {
	state := s.Eng.SaveJSON(company)
	slot <- struct{}{}
	defer func() { <-slot }()
	type pick struct {
		key, kind string
		neck      float64
	}
	var picks []pick
	queue := map[string][]engine.ExtOrder{}
	per := map[string]int{}
	for _, p := range m.Plants {
		for _, l := range p.Lines {
			k := kind(p.ID, l)
			if l.Neck <= 0 || l.Neck > probeMaxCycle || per[k] >= 2 {
				continue
			}
			part := l.Last
			if l.Running != nil {
				part = l.Running.Part
			}
			if part == "" && len(l.Parts) > 0 {
				part = l.Parts[0].PartNo
			}
			per[k]++
			key := p.ID + "/" + l.ID
			picks = append(picks, pick{key, k, l.Neck})
			queue[key] = []engine.ExtOrder{{No: "PROBE-" + key, PartNo: part, Qty: math.MaxInt32}}
		}
	}
	days := probeDays
	if s.ProbeDays > 0 {
		days = s.ProbeDays
	}
	p := &Probed{At: time.UnixMilli(m.Now), Hours: days * 24, Eff: map[string]float64{}, sig: sig}
	if len(picks) == 0 {
		return p, nil
	}
	run, err := s.run(company, state, queue, p.Hours, true)
	if err != nil {
		return nil, err
	}
	start := map[string]time.Time{}
	for _, e := range run.Events {
		if e.Kind == "start" && strings.HasPrefix(e.No, "PROBE-") {
			start[e.No] = time.UnixMilli(e.At)
		}
	}
	good := map[string]int{}
	for _, l := range run.Left {
		if strings.HasPrefix(l.No, "PROBE-") {
			good[l.No] = l.Good
		}
	}
	end := time.UnixMilli(run.End)
	cals := map[string]*Calendar{}
	crew := map[string]bool{}
	nos := map[string]int{}
	for _, pl := range m.Plants {
		for _, l := range pl.Lines {
			cals[pl.ID+"/"+l.ID] = calendarOf(pl.Calendar)
			crew[pl.ID+"/"+l.ID] = l.Crew
			nos[pl.ID+"/"+l.ID] = l.No
		}
	}
	sum, n := map[string]float64{}, map[string]int{}
	for _, pk := range picks {
		no := "PROBE-" + pk.key
		st, ok := start[no]
		if !ok || !end.After(st.Add(8*time.Hour)) {
			continue
		}
		tl := build(cals[pk.key], crew[pk.key], nos[pk.key], nil, st.Unix(), int(end.Sub(st).Hours()/24)+2)
		work := tl.work(st.Unix(), end.Unix())
		if work < 6*3600 || good[no] < 20 {
			continue
		}
		eff := float64(good[no]) * pk.neck / work
		sum[pk.kind] += eff
		n[pk.kind]++
	}
	p.Runs = 1
	for k, v := range sum {
		p.Eff[k] = math.Min(1, math.Max(0.15, v/float64(n[k])))
	}
	if prev != nil && prev.sig == sig && len(prev.Eff) > 0 {
		w := float64(min(prev.Runs, 3))
		for k, old := range prev.Eff {
			if cur, ok := p.Eff[k]; ok {
				p.Eff[k] = (old*w + cur) / (w + 1)
			} else {
				p.Eff[k] = old
			}
		}
		p.Runs = min(prev.Runs+1, 4)
	}
	return p, nil
}
