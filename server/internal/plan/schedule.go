package plan

import (
	"cmp"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"
)

// weights are what the goal makes a plan cost: per hour late, per late order, per hour of
// changeover and per changeover.
type weights struct{ late, lateOrder, setupHour, setup float64 }

var goalWeights = map[Goal]weights{
	Due:     {late: 10, lateOrder: 60, setupHour: 1, setup: 0.5},
	Balance: {late: 4, lateOrder: 20, setupHour: 4, setup: 4},
	Setups:  {late: 1, lateOrder: 5, setupHour: 10, setup: 25},
}

// batch is how far past an order's due date a later order of the same part may be pulled up
// behind it on the same line while the plan is first laid out.
var batch = map[Goal]int64{Due: 0, Balance: 24 * 3600, Setups: 7 * 24 * 3600}

// budget is how many sequences the local search may try: fixed, not timed, so the same input
// always gives the same plan.
const budget = 2500

type iv struct{ from, to int64 }

type dieInfo struct {
	Die
	busy []iv
}

type locked struct {
	o  int
	s0 int64
}

type problem struct {
	in      *Input
	now     int64
	lines   []*Line
	key     map[string]int
	tl      []*timeline
	ords    []*Order
	elig    [][]int     // order → line indexes it can run on
	proc    [][]float64 // order → line → seconds of work (NaN: cannot)
	release []int64
	due     []int64   // 0: none
	pools   [][][]int // order → line → pools (each a list of die indexes) it needs one of
	dies    []dieInfo
	noDie   [][]string // order → kinds it has dies for but none usable
	w       weights
	locks   [][]locked // line → locked orders by start
	free    []int      // orders to sequence
	held    []int
	none    []int // no line makes it
}

// newProblem prepares an input for scheduling.
func newProblem(in *Input) *problem {
	p := &problem{in: in, now: in.Now.Unix(), key: map[string]int{}, w: goalWeights[in.Goal]}
	if p.w == (weights{}) {
		p.w = goalWeights[Due]
	}
	days := cmp.Or(in.Days, 90)
	for i := range in.Lines {
		l := &in.Lines[i]
		p.lines = append(p.lines, l)
		p.key[l.Key] = i
		var stops []Stop
		for _, s := range in.Stops {
			if s.Line == l.Key || s.Line == "" {
				stops = append(stops, s)
			}
		}
		p.tl = append(p.tl, build(l.Cal, l.Crew, l.No, stops, p.now, days))
	}
	for i := range in.Dies {
		p.dies = append(p.dies, dieInfo{Die: in.Dies[i]})
	}
	n := len(in.Orders)
	p.elig, p.proc, p.pools, p.noDie = make([][]int, n), make([][]float64, n), make([][][]int, n), make([][]string, n)
	p.release, p.due = make([]int64, n), make([]int64, n)
	p.locks = make([][]locked, len(p.lines))
	for i := range in.Orders {
		o := &in.Orders[i]
		p.ords = append(p.ords, o)
		p.proc[i] = make([]float64, len(p.lines))
		p.pools[i] = make([][]int, len(p.lines))
		for j := range p.proc[i] {
			p.proc[i][j] = math.NaN()
		}
		for _, k := range o.Lines {
			if j, ok := p.key[k]; ok && !slices.Contains(p.elig[i], j) {
				p.elig[i] = append(p.elig[i], j)
				p.proc[i][j] = float64(o.Qty) * p.lines[j].Rate
				p.pools[i][j], p.noDie[i] = p.diePools(o, p.lines[j], p.noDie[i])
			}
		}
		p.release[i] = max(p.now, o.Release.Unix())
		if !o.Due.IsZero() {
			p.due[i] = o.Due.Unix()
		}
		switch {
		case len(p.elig[i]) == 0:
			p.none = append(p.none, i)
		case o.Hold:
			p.held = append(p.held, i)
		case o.Lock != nil && p.lockable(i):
			j := p.key[o.Lock.Line]
			p.locks[j] = append(p.locks[j], locked{o: i, s0: max(p.now, o.Lock.Start.Unix())})
		default:
			p.free = append(p.free, i)
		}
	}
	for j := range p.locks {
		slices.SortStableFunc(p.locks[j], func(a, b locked) int { return cmp.Compare(a.s0, b.s0) })
	}
	return p
}

func (p *problem) lockable(i int) bool {
	j, ok := p.key[p.ords[i].Lock.Line]
	return ok && slices.Contains(p.elig[i], j)
}

// diePools are, for each kind of die a line's machines take, the dies of the order's part (or,
// when none is booked to the part, of its product) it can run with. A kind the stores keep dies of
// for the part but none usable is noted, and does not hold the order back.
func (p *problem) diePools(o *Order, l *Line, noDie []string) ([]int, []string) {
	match := func(d Die) bool { return strings.EqualFold(d.Product, o.Part) }
	if !slices.ContainsFunc(p.dies, func(d dieInfo) bool { return match(d.Die) }) {
		match = func(d Die) bool {
			return slices.ContainsFunc(o.Family, func(f string) bool { return f != "" && strings.EqualFold(d.Product, f) })
		}
	}
	var pools []int
	for _, k := range l.Kinds {
		var ids []int
		any := false
		for di, d := range p.dies {
			if d.Kind == k && match(d.Die) {
				any = true
				if d.Left > 0 {
					ids = append(ids, di)
				}
			}
		}
		if any && len(ids) == 0 && !slices.Contains(noDie, k) {
			noDie = append(noDie, k)
		}
		if len(ids) > 0 {
			// a pool is a list of dies; encode as a negative-terminated run in one slice
			pools = append(pools, append(ids, -1)...)
		}
	}
	return pools, noDie
}

// eval is a decoded sequence: when each order runs and what the plan costs.
type eval struct {
	line, seq         []int
	s0, start, end    []int64
	setup             []float64
	dieWait, lineWait []int64
	dies              [][]int
	dieShort, dieLow  [][]string
	cost              float64
	locksMoved        []int
	runEnd            []int64 // line → end of the order it runs now
	lastEnd           []int64
}

func (p *problem) changeover(j int, last, part string) float64 {
	if last == "" || strings.EqualFold(last, part) {
		return 0
	}
	return p.lines[j].Setup
}

// fitDie is the earliest moment at or after x a die is free for an order that would end at end(x).
func fitDie(d *dieInfo, x int64, end func(int64) int64) int64 {
	for range 64 {
		e := end(x)
		moved := false
		for _, b := range d.busy {
			if b.from < e && b.to > x {
				x = b.to
				moved = true
			}
		}
		if !moved {
			return x
		}
	}
	return x
}

// decode lays out each line's sequence in time, all lines together so that a die goes to the
// line that asks for it first: the line whose next order could start earliest goes next.
func (p *problem) decode(seq [][]int, full bool) *eval {
	n, L := len(p.ords), len(p.lines)
	e := &eval{line: make([]int, n), seq: make([]int, n), s0: make([]int64, n), start: make([]int64, n), end: make([]int64, n),
		setup: make([]float64, n), dieWait: make([]int64, n), lineWait: make([]int64, n), runEnd: make([]int64, L), lastEnd: make([]int64, L)}
	for i := range e.line {
		e.line[i] = -1
	}
	if full {
		e.dies, e.dieShort, e.dieLow = make([][]int, n), make([][]string, n), make([][]string, n)
	}
	for d := range p.dies {
		p.dies[d].busy = p.dies[d].busy[:0]
	}
	left := make([]int64, len(p.dies))
	for d := range p.dies {
		left[d] = p.dies[d].Left
	}
	lt := make([]int64, L)
	last := make([]string, L)
	for j, l := range p.lines {
		lt[j] = p.now
		last[j] = l.Last
		if r := l.Running; r != nil {
			end := p.tl[j].advance(p.tl[j].next(p.now), float64(r.Left)*l.Rate)
			e.runEnd[j] = end
			lt[j] = end
			last[j] = r.Part
			// the dies on the line are busy with it
			for d := range p.dies {
				if p.dies[d].Line == l.Key {
					p.dies[d].busy = append(p.dies[d].busy, iv{p.now, end})
				}
			}
		}
	}
	// the locked orders hold their dies from their start
	for j := range p.locks {
		for _, lk := range p.locks[j] {
			pools := p.pools[lk.o][j]
			end := p.tl[j].advance(p.tl[j].advance(lk.s0, p.lines[j].Setup), p.proc[lk.o][j])
			for _, pool := range splitPools(pools) {
				best := p.pickDie(pool, lk.s0, func(int64) int64 { return end }, p.lines[j].Key)
				p.dies[best].busy = append(p.dies[best].busy, iv{lk.s0, end})
			}
		}
	}
	pos := make([]int, L)
	lp := make([]int, L)
	cnt := make([]int, L)
	place := func(i, j int, s0 int64, setup float64, dieWait, lineWait int64, dies []int) {
		start := p.tl[j].advance(s0, setup)
		end := p.tl[j].advance(start, p.proc[i][j])
		cnt[j]++
		e.line[i], e.seq[i], e.s0[i], e.start[i], e.end[i], e.setup[i], e.dieWait[i], e.lineWait[i] = j, cnt[j], s0, start, end, setup, dieWait, lineWait
		lt[j], last[j] = end, p.ords[i].Part
		qty := int64(p.ords[i].Qty)
		for _, d := range dies {
			p.dies[d].busy = append(p.dies[d].busy, iv{s0, end})
			if full {
				dd := p.dies[d]
				if left[d] < qty {
					e.dieShort[i] = append(e.dieShort[i], fmt.Sprintf("模具 %s 只剩 %d 次，不够这单 %d 件，中途要换模", dd.No, max(left[d], 0), qty))
				} else if dd.Life > 0 && float64(left[d]-qty) < 0.1*float64(dd.Life) {
					e.dieLow[i] = append(e.dieLow[i], fmt.Sprintf("模具 %s 做完这单只剩 %d 次（%.0f%%），备好替换模", dd.No, left[d]-qty, 100*float64(left[d]-qty)/float64(dd.Life)))
				}
			}
			left[d] -= qty
		}
		if full {
			e.dies[i] = dies
		}
	}
	placeLock := func(j int) {
		lk := p.locks[j][lp[j]]
		lp[j]++
		s0 := max(lk.s0, lt[j])
		if s0 > lk.s0 {
			e.locksMoved = append(e.locksMoved, lk.o)
		}
		place(lk.o, j, s0, p.changeover(j, last[j], p.ords[lk.o].Part), 0, 0, nil)
	}
	for {
		best, bestT := -1, int64(math.MaxInt64)
		for j := range L {
			var t int64 = math.MaxInt64
			if pos[j] < len(seq[j]) {
				t = max(lt[j], p.release[seq[j][pos[j]]])
			}
			if lp[j] < len(p.locks[j]) {
				t = min(t, max(p.locks[j][lp[j]].s0, lt[j]))
			}
			if t < bestT {
				best, bestT = j, t
			}
		}
		if best < 0 {
			break
		}
		j := best
		if pos[j] >= len(seq[j]) || (lp[j] < len(p.locks[j]) && max(p.locks[j][lp[j]].s0, lt[j]) <= max(lt[j], p.release[seq[j][pos[j]]])) {
			placeLock(j)
			continue
		}
		i := seq[j][pos[j]]
		pos[j]++
		ready := max(lt[j], p.release[i])
		t := ready
		var dies []int
		var setup float64
		var s0 int64
		for range 32 {
			s0 = p.tl[j].next(t)
			setup = p.changeover(j, last[j], p.ords[i].Part)
			endAt := func(x int64) int64 { return p.tl[j].advance(p.tl[j].advance(p.tl[j].next(x), setup), p.proc[i][j]) }
			dies = dies[:0]
			x := s0
			for _, pool := range splitPools(p.pools[i][j]) {
				d := p.pickDie(pool, s0, endAt, p.lines[j].Key)
				dies = append(dies, d)
				x = max(x, fitDie(&p.dies[d], s0, endAt))
			}
			if x > s0 {
				t = x
				continue
			}
			// it would run into a locked order: that one goes first, this one after it
			if lp[j] < len(p.locks[j]) && endAt(s0) > p.locks[j][lp[j]].s0 {
				placeLock(j)
				t = max(t, lt[j])
				continue
			}
			break
		}
		dieWait := s0 - p.tl[j].next(ready)
		place(i, j, s0, setup, max(dieWait, 0), max(ready-max(p.now, p.release[i]), 0), dies)
	}
	copy(e.lastEnd, lt)
	e.cost = p.cost(e)
	return e
}

func splitPools(enc []int) [][]int {
	var out [][]int
	start := 0
	for k, v := range enc {
		if v < 0 {
			out = append(out, enc[start:k])
			start = k + 1
		}
	}
	return out
}

// pickDie is the die of a pool free soonest for an order from s0: the one on the line, then the
// one with most strokes left, then by number.
func (p *problem) pickDie(pool []int, s0 int64, end func(int64) int64, line string) int {
	best := pool[0]
	bt := fitDie(&p.dies[best], s0, end)
	for _, d := range pool[1:] {
		t := fitDie(&p.dies[d], s0, end)
		a, b := p.dies[d], p.dies[best]
		if t < bt || (t == bt && ((a.Line == line) != (b.Line == line) && a.Line == line ||
			(a.Line == line) == (b.Line == line) && (a.Left > b.Left || a.Left == b.Left && a.No < b.No))) {
			best, bt = d, t
		}
	}
	return best
}

func (p *problem) cost(e *eval) float64 {
	var c float64
	for i := range p.ords {
		j := e.line[i]
		if j < 0 {
			continue
		}
		if d := p.due[i]; d > 0 && e.end[i] > d {
			c += p.w.late*float64(e.end[i]-d)/3600 + p.w.lateOrder
		}
		if e.setup[i] > 0 {
			c += p.w.setupHour*e.setup[i]/3600 + p.w.setup
		}
		// a little for finishing early: of two plans alike, the tighter
		c += 1e-3 * float64(e.end[i]-p.now) / 3600
	}
	return c
}

// construct lays out a first plan: orders by due date, each on the line that would finish it
// first (changeovers weighed by the goal), with later orders of the same part pulled up behind it.
func (p *problem) construct() [][]int {
	L := len(p.lines)
	seq := make([][]int, L)
	lt := make([]int64, L)
	last := make([]string, L)
	for j, l := range p.lines {
		lt[j], last[j] = p.now, l.Last
		if r := l.Running; r != nil {
			lt[j] = p.tl[j].advance(p.tl[j].next(p.now), float64(r.Left)*l.Rate)
			last[j] = r.Part
		}
		// a line's locked orders take its time first
		for _, lk := range p.locks[j] {
			lt[j] = max(lt[j], p.tl[j].advance(p.tl[j].advance(lk.s0, l.Setup), p.proc[lk.o][j]))
		}
	}
	order := slices.Clone(p.free)
	p.byDue(order)
	done := make([]bool, len(p.ords))
	lambda := map[Goal]float64{Due: 0, Balance: 1, Setups: 3}[p.in.Goal]
	put := func(i, j int) {
		s := p.tl[j].next(max(lt[j], p.release[i]))
		lt[j] = p.tl[j].advance(p.tl[j].advance(s, p.changeover(j, last[j], p.ords[i].Part)), p.proc[i][j])
		last[j] = p.ords[i].Part
		seq[j] = append(seq[j], i)
		done[i] = true
	}
	for k, i := range order {
		if done[i] {
			continue
		}
		best, bestScore := -1, math.Inf(1)
		for _, j := range p.elig[i] {
			s := p.tl[j].next(max(lt[j], p.release[i]))
			setup := p.changeover(j, last[j], p.ords[i].Part)
			end := p.tl[j].advance(p.tl[j].advance(s, setup), p.proc[i][j])
			score := float64(end) + lambda*setup
			if score < bestScore {
				best, bestScore = j, score
			}
		}
		put(i, best)
		if w := batch[p.in.Goal]; w > 0 || p.due[i] == 0 {
			for _, i2 := range order[k+1:] {
				if done[i2] || !strings.EqualFold(p.ords[i2].Part, p.ords[i].Part) || math.IsNaN(p.proc[i2][best]) {
					continue
				}
				if d1, d2 := p.due[i], p.due[i2]; d1 == 0 || d2 != 0 && d2 <= d1+w {
					put(i2, best)
				}
			}
		}
	}
	return seq
}

// byDue sorts orders by due date (none last), then when they came, then number.
func (p *problem) byDue(xs []int) {
	slices.SortStableFunc(xs, func(a, b int) int {
		da, db := p.due[a], p.due[b]
		if da == 0 {
			da = math.MaxInt64
		}
		if db == 0 {
			db = math.MaxInt64
		}
		return cmp.Or(cmp.Compare(da, db), p.ords[a].Created.Compare(p.ords[b].Created), cmp.Compare(p.ords[a].No, p.ords[b].No))
	})
}

// improve moves and swaps orders while that makes the plan cheaper, trying a fixed number of
// sequences at most. Moves, for each order in turn: swap with the next on its line; move behind
// an order of the same part (on any line it can run on); a late order a few places earlier or
// onto another line at its due-date place.
func (p *problem) improve(seq [][]int) ([][]int, *eval, int) {
	cur := p.decode(seq, false)
	evals := 1
	try := func(next [][]int) bool {
		if evals >= budget {
			return false
		}
		evals++
		e := p.decode(next, false)
		if e.cost < cur.cost-1e-9 {
			seq, cur = next, e
			return true
		}
		return false
	}
	for pass := 0; pass < 6 && evals < budget; pass++ {
		better := false
		for j := 0; j < len(seq) && evals < budget; j++ {
			for k := 0; k < len(seq[j]) && evals < budget; k++ {
				i := seq[j][k]
				// swap with the next
				if k+1 < len(seq[j]) {
					if try(swapped(seq, j, k)) {
						better = true
						continue
					}
				}
				// behind an order of the same part
				moved := false
				for _, j2 := range p.elig[i] {
					for k2 := len(seq[j2]) - 1; k2 >= 0 && !moved; k2-- {
						o2 := seq[j2][k2]
						if o2 == i || !strings.EqualFold(p.ords[o2].Part, p.ords[i].Part) {
							continue
						}
						if j2 == j && (k2 == k-1) {
							break
						}
						if try(moved2(seq, j, k, j2, k2+1)) {
							moved = true
						}
						break
					}
					if moved {
						break
					}
				}
				if moved {
					better = true
					continue
				}
				// a late order: earlier, or on another line
				if d := p.due[i]; d > 0 && cur.end[i] > d {
					for back := 1; back <= 4 && k-back >= 0 && !moved; back++ {
						if try(moved2(seq, j, k, j, k-back)) {
							moved = true
						}
					}
					for _, j2 := range p.elig[i] {
						if moved || j2 == j {
							continue
						}
						at := 0
						for at < len(seq[j2]) && p.due[seq[j2][at]] != 0 && p.due[seq[j2][at]] <= d {
							at++
						}
						if try(moved2(seq, j, k, j2, at)) {
							moved = true
						}
					}
				}
				if moved {
					better = true
				}
			}
		}
		if !better {
			break
		}
	}
	return seq, cur, evals
}

func clone(seq [][]int) [][]int {
	out := make([][]int, len(seq))
	for j := range seq {
		out[j] = slices.Clone(seq[j])
	}
	return out
}

func swapped(seq [][]int, j, k int) [][]int {
	out := clone(seq)
	out[j][k], out[j][k+1] = out[j][k+1], out[j][k]
	return out
}

// moved2 moves the order at seq[j][k] to line j2 before position at (counted before the move).
func moved2(seq [][]int, j, k, j2, at int) [][]int {
	out := clone(seq)
	i := out[j][k]
	out[j] = slices.Delete(out[j], k, k+1)
	if j2 == j && at > k {
		at--
	}
	at = min(max(at, 0), len(out[j2]))
	out[j2] = slices.Insert(out[j2], at, i)
	return out
}

// Schedule makes a plan.
func Schedule(in *Input) *Result {
	t0 := time.Now()
	p := newProblem(in)
	seq, e, evals := p.improve(p.construct())
	r := p.result(seq, e)
	r.Evals, r.Millis = evals, time.Since(t0).Milliseconds()
	return r
}

// Evaluate lays out a given order of work as it stands, without improving it: orders it does
// not name go after the line's own, by due date, on the first line that can make them.
func Evaluate(in *Input, s Seq) *Result {
	p := newProblem(in)
	seq := p.fromSeq(s)
	return p.result(seq, p.decode(seq, false))
}

func (p *problem) fromSeq(s Seq) [][]int {
	seq := make([][]int, len(p.lines))
	at := map[string]int{}
	for i, o := range p.ords {
		at[o.No] = i
	}
	placed := make([]bool, len(p.ords))
	free := make([]bool, len(p.ords))
	for _, i := range p.free {
		free[i] = true
	}
	for j, l := range p.lines {
		for _, no := range s[l.Key] {
			if i, ok := at[no]; ok && free[i] && !placed[i] && !math.IsNaN(p.proc[i][j]) {
				seq[j] = append(seq[j], i)
				placed[i] = true
			}
		}
	}
	var rest []int
	for _, i := range p.free {
		if !placed[i] {
			rest = append(rest, i)
		}
	}
	p.byDue(rest)
	for _, i := range rest {
		seq[p.elig[i][0]] = append(seq[p.elig[i][0]], i)
	}
	return seq
}

// Move takes an order from where it is in s to line at position at (0 first) and lays the plan
// out again as it then stands.
func Move(in *Input, s Seq, no, line string, at int) (*Result, error) {
	p := newProblem(in)
	i := slices.IndexFunc(p.ords, func(o *Order) bool { return o.No == no })
	if i < 0 {
		return nil, fmt.Errorf("计划里没有工单 %s", no)
	}
	j, ok := p.key[line]
	if !ok {
		return nil, fmt.Errorf("没有产线 %s", line)
	}
	if math.IsNaN(p.proc[i][j]) {
		return nil, fmt.Errorf("%s 做不了工单 %s 的 %s", p.lines[j].Name, no, p.ords[i].Part)
	}
	if !slices.Contains(p.free, i) {
		return nil, fmt.Errorf("工单 %s 锁定了、在做或缺料暂缓，挪不了", no)
	}
	seq := p.fromSeq(s)
	for j0 := range seq {
		if k := slices.Index(seq[j0], i); k >= 0 {
			seq = moved2(seq, j0, k, j, at)
			break
		}
	}
	return p.result(seq, p.decode(seq, false)), nil
}

func hours(s int64) string {
	h := float64(s) / 3600
	if h < 1 {
		return fmt.Sprintf("%.0f 分钟", h*60)
	}
	if h < 48 {
		return fmt.Sprintf("%.1f 小时", h)
	}
	return fmt.Sprintf("%.1f 天", h/24)
}

func clock(t int64) string { return time.Unix(t, 0).In(cst).Format("01-02 15:04") }

var cst = time.FixedZone("CST", local)

// result writes a decoded plan out: each order with its times and why it is late, the held ones,
// the figures.
func (p *problem) result(seq [][]int, e *eval) *Result {
	e = p.decode(seq, true)
	r := &Result{Goal: p.in.Goal, Now: p.in.Now, Orders: []Planned{}, Held: []Planned{}, Lines: []LineKPI{}}
	planned := func(i int) Planned {
		o := p.ords[i]
		x := Planned{No: o.No, Part: o.Part, Name: o.Name, Customer: o.Customer, Qty: o.Qty, Due: o.Due}
		for _, j := range p.elig[i] {
			x.Lines = append(x.Lines, p.lines[j].Key)
		}
		x.Arrive = o.Arrive
		if o.Short != "" {
			text := "缺料：" + o.Short
			switch {
			case !o.Release.IsZero() && o.Release.After(p.in.Now):
				from := "预计"
				if a := o.Arrive; a != nil && a.From == Purchase {
					from = "按" + a.Text + "，预计"
				} else if a != nil && a.From == Manual {
					from = "手填预计"
				}
				text += "，" + from + " " + clock(o.Release.Unix()) + " 到料"
			case !o.Hold:
				text += "（照排，料不到开不了工）"
			}
			x.Flags = append(x.Flags, Why{"material", text})
		}
		for _, k := range p.noDie[i] {
			x.Flags = append(x.Flags, Why{"die", fmt.Sprintf("没有能用的%s（在修或用完），按不缺模具排", k)})
		}
		return x
	}
	for j, l := range p.lines {
		if run := l.Running; run != nil {
			x := Planned{No: run.No, Part: run.Part, Name: run.Name, Customer: run.Customer, Qty: run.Left, Due: run.Due, Line: l.Key, LineName: l.Name, Running: true,
				SetupStart: p.in.Now, Start: p.in.Now, End: time.Unix(e.runEnd[j], 0)}
			if !run.Due.IsZero() && e.runEnd[j] > run.Due.Unix() {
				x.Late, x.LateMin = true, math.Round(float64(e.runEnd[j]-run.Due.Unix())/60)
				x.Why = []Why{{"capacity", fmt.Sprintf("在做，剩下 %d 件要做到 %s", run.Left, clock(e.runEnd[j]))}}
			}
			r.Orders = append(r.Orders, x)
		}
	}
	var lateMin float64
	for i := range p.ords {
		j := e.line[i]
		if j < 0 {
			continue
		}
		x := planned(i)
		l := p.lines[j]
		x.Line, x.LineName, x.Seq = l.Key, l.Name, e.seq[i]
		x.SetupStart, x.Start, x.End = time.Unix(e.s0[i], 0), time.Unix(e.start[i], 0), time.Unix(e.end[i], 0)
		x.SetupMin = math.Round(e.setup[i] / 60)
		x.Locked = p.ords[i].Lock != nil && slices.ContainsFunc(p.locks[j], func(lk locked) bool { return lk.o == i })
		for _, d := range e.dies[i] {
			x.Dies = append(x.Dies, p.dies[d].No)
		}
		for _, s := range e.dieShort[i] {
			x.Flags = append(x.Flags, Why{"die", s})
		}
		for _, s := range e.dieLow[i] {
			x.Flags = append(x.Flags, Why{"die", s})
		}
		if slices.Contains(e.locksMoved, i) {
			x.Flags = append(x.Flags, Why{"capacity", "锁定的开工时间被前一单占了，往后顺延"})
		}
		if l.Down != "" {
			x.Flags = append(x.Flags, Why{"down", l.Name + "现在停着：" + l.Down})
		}
		if d := p.due[i]; d > 0 && e.end[i] > d {
			x.Late = true
			x.LateMin = math.Round(float64(e.end[i]-d) / 60)
			lateMin += x.LateMin
			x.Why = p.why(i, j, e)
		}
		r.Orders = append(r.Orders, x)
	}
	for _, i := range p.held {
		x := planned(i)
		x.Held = true
		text := "缺料，又不知道什么时候到料：先不排。录一张采购单，或者填上预计到料时间，或者改成「缺料也照排」"
		if a := p.ords[i].Arrive; a != nil && a.Text != "" {
			text = "缺料，" + a.Text + "：先不排。录一张采购单，或者填上预计到料时间，或者改成「缺料也照排」"
		}
		x.Why = []Why{{"material", text}}
		r.Held = append(r.Held, x)
	}
	for _, i := range p.none {
		x := planned(i)
		x.Why = []Why{{"capacity", "没有产线能做这个料号"}}
		r.Unplaceable = append(r.Unplaceable, x)
	}
	slices.SortStableFunc(r.Orders, func(a, b Planned) int {
		return cmp.Or(cmp.Compare(p.key[a.Line], p.key[b.Line]), cmp.Compare(a.Seq, b.Seq))
	})
	// the figures
	k := &r.KPI
	var end int64 = p.now
	for _, x := range r.Orders {
		end = max(end, x.End.Unix())
	}
	horizon := max(end, p.now+24*3600)
	var utilSum float64
	var utilN int
	for j, l := range p.lines {
		lk := LineKPI{Key: l.Key, Name: l.Name, PlantName: l.PlantName, Rate: l.Rate, RateSource: l.RateSource, Setup: math.Round(l.Setup / 60), SetupSrc: l.SetupSource, Down: l.Down}
		var busy float64
		if l.Running != nil {
			busy += p.tl[j].work(p.now, e.runEnd[j])
			lk.Orders++
			lk.BusyUntil = time.Unix(e.runEnd[j], 0)
		}
		for i := range p.ords {
			if e.line[i] != j {
				continue
			}
			lk.Orders++
			busy += e.setup[i] + p.proc[i][j]
			if e.setup[i] > 0 {
				lk.Setups++
				lk.SetupHours += e.setup[i] / 3600
			}
			if t := time.Unix(e.end[i], 0); t.After(lk.BusyUntil) {
				lk.BusyUntil = t
			}
		}
		if avail := p.tl[j].work(p.now, horizon); avail > 0 {
			lk.Util = math.Min(1, busy/avail)
		}
		lk.SetupHours = math.Round(lk.SetupHours*10) / 10
		if lk.Orders > 0 {
			utilSum += lk.Util
			utilN++
		}
		lk.Util = math.Round(lk.Util*1000) / 1000
		k.Setups += lk.Setups
		k.SetupHours += lk.SetupHours
		r.Lines = append(r.Lines, lk)
	}
	for _, x := range r.Orders {
		if x.Late {
			k.Late++
		} else {
			k.OnTime++
		}
	}
	k.Planned = len(r.Orders)
	k.Held = len(r.Held)
	if k.Planned > 0 {
		k.OnTimePct = math.Round(1000*float64(k.OnTime)/float64(k.Planned)) / 10
	}
	k.LateHours = math.Round(lateMin/6) / 10
	k.SetupHours = math.Round(k.SetupHours*10) / 10
	if utilN > 0 {
		k.Util = math.Round(1000*utilSum/float64(utilN)) / 1000
	}
	if end > p.now {
		k.End = time.Unix(end, 0)
	}
	k.Cost = math.Round(e.cost*100) / 100
	return r
}

// why an order is late: its due date is too close to make it even now, it waits for material,
// a die, the orders ahead of it on the line, a changeover.
func (p *problem) why(i, j int, e *eval) []Why {
	var out []Why
	l := p.lines[j]
	from := max(p.now, p.release[i])
	fastest := p.tl[j].advance(p.tl[j].next(from), p.proc[i][j])
	if d := p.due[i]; fastest > d {
		if from > p.now {
			out = append(out, Why{"material", fmt.Sprintf("要等料到 %s 才能开工，最快也要 %s 做完", clock(from), clock(fastest))})
		} else {
			out = append(out, Why{"due", fmt.Sprintf("交期太紧：%d 件在%s要做 %s，就算现在开工也要 %s 做完", p.ords[i].Qty, l.Name, hours(int64(p.proc[i][j])), clock(fastest))})
		}
	} else if from > p.now {
		out = append(out, Why{"material", fmt.Sprintf("等料到 %s", clock(from))})
	}
	if w := e.lineWait[i]; w > 600 {
		out = append(out, Why{"capacity", fmt.Sprintf("%s前面排满了，要等 %s 才轮到", l.Name, hours(w))})
	}
	if w := e.dieWait[i]; w > 600 {
		var names []string
		for _, d := range e.dies[i] {
			names = append(names, p.dies[d].No)
		}
		out = append(out, Why{"die", fmt.Sprintf("模具 %s 在别的线上用，要等 %s", strings.Join(names, "、"), hours(w))})
	}
	if e.setup[i] > 0 {
		out = append(out, Why{"setup", fmt.Sprintf("开工前换型 %.0f 分钟", e.setup[i]/60)})
	}
	return out
}
