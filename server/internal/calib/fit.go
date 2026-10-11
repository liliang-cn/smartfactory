// Package calib fits a machine's simulation parameters to what it measured: temperature rise
// idle and running and how fast it gets there, idle and running power, vibration, cycle time,
// and how often it breaks down and how long a repair takes. The twin keeps running its model
// beside the measurements (residuals); a fitted calibration replaces a machine's template values
// once an administrator applies it, and can be taken back.
package calib

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// The model the sim runs (web/src/sim/plant.ts, the machine update), restated so a stored period
// can be played back with any parameters.
const ambient = 22.0

// mode is what a machine is doing, as its state words say.
type mode int

const (
	off  mode = iota // stopped, fault, maintenance, changeover: no heat, almost no power
	idle             // on, not making parts (idle, blocked)
	run
)

func modeOf(state string) (mode, bool) {
	switch state {
	case "running":
		return run, true
	case "idle", "blocked":
		return idle, true
	case "stopped", "fault", "maint", "changeover":
		return off, true
	}
	return off, false
}

// Sample is one reading.
type Sample struct {
	T time.Time
	V float64
}

// StateChange is the machine saying what it is doing.
type StateChange struct {
	T     time.Time
	State string
}

// Data is a machine's stored period: its readings (flagged ones left out) and state changes.
type Data struct {
	Temp, Power, Vib, Speed, Health, Cycles []Sample
	States                                  []StateChange
}

// timeline answers what the machine was doing at a time, and how fast and healthy it was.
type timeline struct {
	states []StateChange
	speed  []Sample
	health []Sample
}

func last(xs []Sample, t time.Time, def float64) float64 {
	i := sort.Search(len(xs), func(i int) bool { return xs[i].T.After(t) })
	if i == 0 {
		return def
	}
	return xs[i-1].V
}

func (tl timeline) at(t time.Time) (m mode, maint bool, known bool) {
	i := sort.Search(len(tl.states), func(i int) bool { return tl.states[i].T.After(t) })
	if i == 0 {
		return off, false, false
	}
	s := tl.states[i-1].State
	m, known = modeOf(s)
	return m, s == "maint", known
}

func (tl timeline) speedAt(t time.Time) float64 {
	return math.Max(0.5, math.Min(1.5, last(tl.speed, t, 1)))
}

func (tl timeline) healthAt(t time.Time) float64 {
	return math.Max(0, math.Min(1, last(tl.health, t, 1)))
}

// Thermal is a first-order temperature model: idle and running rise over ambient and the time
// constant it approaches them with.
type Thermal struct{ Idle, Run, Tau float64 }

// Power is idle draw and what running adds at full speed.
type Power struct{ Idle, Run float64 }

func powerModel(p Power, m mode, maint bool, speed float64) float64 {
	switch m {
	case run:
		return p.Idle + p.Run*speed
	case idle:
		return p.Idle
	}
	if maint {
		return 0
	}
	return 0.1
}

func vibModel(v float64, m mode, speed, health float64) float64 {
	switch m {
	case run:
		return v * (0.7 + 0.3*speed) * (1 + (1-health)*2.5)
	case idle:
		return v * 0.06
	}
	return 0
}

// coef is a temperature as a linear function of the two rises: idle·A + run·B + C.
type coef struct{ a, b, c float64 }

// replayThermal plays the temperature model over the period for a time constant and returns,
// for each temperature sample after the first, its coefficients in the two rises.
func replayThermal(tl timeline, temps []Sample, tau float64) []coef {
	if len(temps) == 0 {
		return nil
	}
	out := make([]coef, 0, len(temps))
	cur := coef{0, 0, temps[0].V}
	t := temps[0].T
	// the state changes inside each gap split it: the target is constant between them
	j := sort.Search(len(tl.states), func(i int) bool { return tl.states[i].T.After(t) })
	for _, s := range temps[1:] {
		for t.Before(s.T) {
			next := s.T
			if j < len(tl.states) && tl.states[j].T.Before(next) {
				next = tl.states[j].T
			}
			m, _, _ := tl.at(t)
			f := 0.6 + 0.4*tl.speedAt(t)
			w := math.Exp(-next.Sub(t).Seconds() / tau)
			var ta, tb, tc float64
			tc = ambient
			switch m {
			case idle:
				ta = f
				tc += (1 - tl.healthAt(t)) * 18
			case run:
				tb = f
				tc += (1 - tl.healthAt(t)) * 18
			}
			cur = coef{w*cur.a + (1-w)*ta, w*cur.b + (1-w)*tb, w*cur.c + (1-w)*tc}
			if j < len(tl.states) && !tl.states[j].T.After(next) {
				j++
			}
			t = next
		}
		out = append(out, cur)
	}
	return out
}

// thermalError is the mean absolute error of the temperature model with these parameters.
func thermalError(tl timeline, temps []Sample, th Thermal) (float64, int) {
	cs := replayThermal(tl, temps, th.Tau)
	var sum float64
	n := 0
	for i, c := range cs {
		if _, _, known := tl.at(temps[i+1].T); !known {
			continue
		}
		sum += math.Abs(c.a*th.Idle + c.b*th.Run + c.c - temps[i+1].V)
		n++
	}
	if n == 0 {
		return math.NaN(), 0
	}
	return sum / float64(n), n
}

// fitThermal finds the time constant (on a grid, then finer around the best) and, for each, the
// two rises by least squares. A rise the period never shows (the machine was never idle) stays
// at the template's.
func fitThermal(tl timeline, temps []Sample, tpl Thermal) (Thermal, bool, bool, bool) {
	type try struct {
		th            Thermal
		sse           float64
		idleOK, runOK bool
	}
	eval := func(tau float64) try {
		th, ok, idleOK, runOK := fitThermalAt(tl, temps, Thermal{tpl.Idle, tpl.Run, tau})
		if !ok {
			return try{sse: math.Inf(1)}
		}
		var sse float64
		for i, c := range replayThermal(tl, temps, tau) {
			if _, _, known := tl.at(temps[i+1].T); !known {
				continue
			}
			d := c.a*th.Idle + c.b*th.Run + c.c - temps[i+1].V
			sse += d * d
		}
		return try{th, sse, idleOK, runOK}
	}
	const steps = 40
	grid := func(k float64) float64 { return 5 * math.Pow(1800.0/5, k/steps) } // 5 s … 30 min
	best, bestK := try{sse: math.Inf(1)}, 0.0
	for k := 0; k <= steps; k++ {
		if t := eval(grid(float64(k))); t.sse < best.sse {
			best, bestK = t, float64(k)
		}
	}
	for k := bestK - 1; k <= bestK+1; k += 0.1 {
		if k < 0 || k > steps {
			continue
		}
		if t := eval(grid(k)); t.sse < best.sse {
			best = t
		}
	}
	if math.IsInf(best.sse, 1) {
		return tpl, false, false, false
	}
	return best.th, true, best.idleOK, best.runOK
}

// span is the time from the first to the last of some readings.
type span struct{ first, last time.Time }

func (s *span) add(t time.Time) {
	if s.first.IsZero() || t.Before(s.first) {
		s.first = t
	}
	if t.After(s.last) {
		s.last = t
	}
}

func (s span) d() time.Duration { return s.last.Sub(s.first) }

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return math.NaN()
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	if len(s)%2 == 1 {
		return s[len(s)/2]
	}
	return (s[len(s)/2-1] + s[len(s)/2]) / 2
}

func quantile(xs []float64, q float64) float64 {
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	return s[int(math.Round(q*float64(len(s)-1)))]
}

// spread is the median absolute deviation over the median: how well one number describes them.
func spread(xs []float64) float64 {
	m := median(xs)
	d := make([]float64, len(xs))
	for i, x := range xs {
		d[i] = math.Abs(x - m)
	}
	return median(d) / math.Max(math.Abs(m), 1e-9)
}

// What a value is fitted from at least: readings, and the time they cover (a few minutes of a
// machine say little about how it behaves over a shift).
const (
	minSpan        = 15 * time.Minute
	minTempSpan    = 30 * time.Minute
	minSamples     = 30
	minTransitions = 3
	minFaults      = 3
	minCycles      = 30
)

func quality(n int, spread float64) string {
	switch {
	case n >= 300 && spread < 0.1:
		return "good"
	case n >= 100 && spread < 0.25:
		return "fair"
	}
	return "poor"
}

// Param is one parameter: the template's value, the fitted one (absent when the data does not
// allow it, Note says why) and the one in force when a calibration applies it.
type Param struct {
	Name     string   `json:"name"`
	Label    string   `json:"label"`
	Unit     string   `json:"unit"`
	Template float64  `json:"template"`
	Fitted   *float64 `json:"fitted,omitempty"`
	Applied  *float64 `json:"applied,omitempty"`
	// N is how many readings (or events) it was fitted from.
	N int `json:"n"`
	// Quality: good | fair | poor
	Quality string `json:"quality,omitempty"`
	Note    string `json:"note,omitempty"`
}

// Replay is how far the model was from the measurements over the fitted period, with the
// template's parameters and with the fitted ones (mean absolute error).
type Replay struct {
	Field  string  `json:"field"`
	Before float64 `json:"before"`
	After  float64 `json:"after"`
	N      int     `json:"n"`
}

// Template are a machine's template values, as fitting compares with them.
type Template struct {
	Thermal              Thermal
	Power                Power
	Vib, Cycle, MTBF     float64
	RepairMin, RepairMax float64
}

func f64(x float64) *float64 { return &x }

func r(x float64, digits int) float64 {
	p := math.Pow(10, float64(digits))
	return math.Round(x*p) / p
}

// Fit fits a machine's parameters from its period.
func Fit(d Data, tpl Template) ([]Param, []Replay) {
	tl := timeline{states: d.States, speed: d.Speed, health: d.Health}
	ps := []Param{
		{Name: "idleRise", Label: "待机温升", Unit: "°C", Template: tpl.Thermal.Idle},
		{Name: "runRise", Label: "运行温升", Unit: "°C", Template: tpl.Thermal.Run},
		{Name: "tau", Label: "升温时间常数", Unit: "秒", Template: tpl.Thermal.Tau},
		{Name: "pIdle", Label: "待机功率", Unit: "kW", Template: tpl.Power.Idle},
		{Name: "pRun", Label: "运行加功率", Unit: "kW", Template: tpl.Power.Run},
		{Name: "vib", Label: "运行振动", Unit: "mm/s", Template: tpl.Vib},
		{Name: "cycle", Label: "节拍", Unit: "秒", Template: tpl.Cycle},
		{Name: "mtbf", Label: "故障间隔", Unit: "小时", Template: tpl.MTBF},
		{Name: "repairMin", Label: "修复时间下限", Unit: "分钟", Template: tpl.RepairMin},
		{Name: "repairMax", Label: "修复时间上限", Unit: "分钟", Template: tpl.RepairMax},
	}
	get := func(name string) *Param {
		for i := range ps {
			if ps[i].Name == name {
				return &ps[i]
			}
		}
		return nil
	}
	var replays []Replay
	if len(d.States) == 0 {
		for i := range ps {
			ps[i].Note = "设备没有上报状态（state），分不出运行和待机"
		}
		return ps, nil
	}

	// power: the median by what the machine was doing
	var pIdle, pRun []float64
	var idleSpan, runSpan span
	for _, s := range d.Power {
		switch m, _, known := tl.at(s.T); {
		case !known:
		case m == idle:
			pIdle = append(pIdle, s.V)
			idleSpan.add(s.T)
		}
	}
	idleFit := median(pIdle)
	if len(pIdle) >= minSamples && idleSpan.d() < minSpan {
		get("pIdle").N = len(pIdle)
		get("pIdle").Note = fmt.Sprintf("待机时的功率读数只覆盖了 %s，至少要 %s", idleSpan.d().Round(time.Second), minSpan)
		idleFit = tpl.Power.Idle
	} else if len(pIdle) >= minSamples {
		p := get("pIdle")
		p.Fitted, p.N, p.Quality = f64(r(idleFit, 2)), len(pIdle), quality(len(pIdle), spread(pIdle))
	} else {
		get("pIdle").N = len(pIdle)
		get("pIdle").Note = fmt.Sprintf("待机时的功率读数只有 %d 个，至少要 %d 个", len(pIdle), minSamples)
		idleFit = tpl.Power.Idle
	}
	for _, s := range d.Power {
		if m, _, known := tl.at(s.T); known && m == run {
			pRun = append(pRun, (s.V-idleFit)/tl.speedAt(s.T))
			runSpan.add(s.T)
		}
	}
	if len(pRun) >= minSamples && runSpan.d() < minSpan {
		get("pRun").N = len(pRun)
		get("pRun").Note = fmt.Sprintf("运行时的功率读数只覆盖了 %s，至少要 %s", runSpan.d().Round(time.Second), minSpan)
	} else if len(pRun) >= minSamples {
		p := get("pRun")
		p.Fitted, p.N, p.Quality = f64(r(median(pRun), 2)), len(pRun), quality(len(pRun), spread(pRun))
	} else {
		get("pRun").N = len(pRun)
		get("pRun").Note = fmt.Sprintf("运行时的功率读数只有 %d 个，至少要 %d 个", len(pRun), minSamples)
	}
	if len(d.Power) > 0 {
		before, after := Power{tpl.Power.Idle, tpl.Power.Run}, Power{tpl.Power.Idle, tpl.Power.Run}
		if v := get("pIdle").Fitted; v != nil {
			after.Idle = *v
		}
		if v := get("pRun").Fitted; v != nil {
			after.Run = *v
		}
		var sb, sa float64
		n := 0
		for _, s := range d.Power {
			m, maint, known := tl.at(s.T)
			if !known {
				continue
			}
			sp := tl.speedAt(s.T)
			sb += math.Abs(powerModel(before, m, maint, sp) - s.V)
			sa += math.Abs(powerModel(after, m, maint, sp) - s.V)
			n++
		}
		if n > 0 {
			replays = append(replays, Replay{"power", r(sb/float64(n), 3), r(sa/float64(n), 3), n})
		}
	}

	// vibration while running, as at full speed and health
	var vRun []float64
	var vibSpan span
	for _, s := range d.Vib {
		if m, _, known := tl.at(s.T); known && m == run {
			sp, h := tl.speedAt(s.T), tl.healthAt(s.T)
			vRun = append(vRun, s.V/((0.7+0.3*sp)*(1+(1-h)*2.5)))
			vibSpan.add(s.T)
		}
	}
	if len(vRun) >= minSamples && vibSpan.d() < minSpan {
		get("vib").N = len(vRun)
		get("vib").Note = fmt.Sprintf("运行时的振动读数只覆盖了 %s，至少要 %s", vibSpan.d().Round(time.Second), minSpan)
	} else if len(vRun) >= minSamples {
		p := get("vib")
		p.Fitted, p.N, p.Quality = f64(r(median(vRun), 3)), len(vRun), quality(len(vRun), spread(vRun))
		var sb, sa float64
		n := 0
		for _, s := range d.Vib {
			m, _, known := tl.at(s.T)
			if !known {
				continue
			}
			sp, h := tl.speedAt(s.T), tl.healthAt(s.T)
			sb += math.Abs(vibModel(tpl.Vib, m, sp, h) - s.V)
			sa += math.Abs(vibModel(*p.Fitted, m, sp, h) - s.V)
			n++
		}
		replays = append(replays, Replay{"vib", r(sb/float64(n), 3), r(sa/float64(n), 3), n})
	} else {
		get("vib").N = len(vRun)
		get("vib").Note = fmt.Sprintf("运行时的振动读数只有 %d 个，至少要 %d 个", len(vRun), minSamples)
	}

	// temperature: played back over the period, the time constant only when the machine changed
	// what it was doing often enough to show it
	transitions := 0
	for i := 1; i < len(d.States); i++ {
		a, _ := modeOf(d.States[i-1].State)
		b, _ := modeOf(d.States[i].State)
		if a != b {
			transitions++
		}
	}
	tempSpan := time.Duration(0)
	if len(d.Temp) > 1 {
		tempSpan = d.Temp[len(d.Temp)-1].T.Sub(d.Temp[0].T)
	}
	if len(d.Temp) >= minSamples && tempSpan < minTempSpan {
		for _, k := range []string{"idleRise", "runRise", "tau"} {
			get(k).N = len(d.Temp)
			get(k).Note = fmt.Sprintf("温度读数只覆盖了 %s，至少要 %s", tempSpan.Round(time.Second), minTempSpan)
		}
	} else if len(d.Temp) >= minSamples {
		th, ok, idleSeen, runSeen := fitThermal(tl, d.Temp, tpl.Thermal)
		if ok {
			if transitions < minTransitions {
				// without changes the time constant cannot be told: fit the rises with the template's
				th, _, idleSeen, runSeen = fitThermalAt(tl, d.Temp, tpl.Thermal)
				get("tau").Note = fmt.Sprintf("状态只切换了 %d 次，至少要 %d 次才看得出升温快慢", transitions, minTransitions)
			} else {
				p := get("tau")
				p.Fitted, p.N, p.Quality = f64(r(th.Tau, 0)), transitions, map[bool]string{true: "good", false: "fair"}[transitions >= 10]
			}
			n := len(d.Temp)
			if idleSeen {
				p := get("idleRise")
				p.Fitted, p.N, p.Quality = f64(r(th.Idle, 1)), n, quality(n, 0)
			} else {
				get("idleRise").Note = "这段时间设备没有待机过"
			}
			if runSeen {
				p := get("runRise")
				p.Fitted, p.N, p.Quality = f64(r(th.Run, 1)), n, quality(n, 0)
			} else {
				get("runRise").Note = "这段时间设备没有运行过"
			}
			before, nb := thermalError(tl, d.Temp, tpl.Thermal)
			after, _ := thermalError(tl, d.Temp, th)
			if nb > 0 {
				replays = append(replays, Replay{"temp", r(before, 2), r(after, 2), nb})
			}
		}
	} else {
		for _, k := range []string{"idleRise", "runRise", "tau"} {
			get(k).N = len(d.Temp)
			get(k).Note = fmt.Sprintf("温度读数只有 %d 个，至少要 %d 个", len(d.Temp), minSamples)
		}
	}

	// breakdowns: running hours between faults and how long each took to clear
	var runSec, healthSum, speedSum float64
	var faults int
	var repairs []float64
	for i, st := range d.States {
		end := time.Time{}
		if i+1 < len(d.States) {
			end = d.States[i+1].T
		}
		m, _ := modeOf(st.State)
		if m == run && !end.IsZero() {
			dur := end.Sub(st.T).Seconds()
			runSec += dur
			healthSum += tl.healthAt(st.T) * dur
			speedSum += tl.speedAt(st.T) * dur
		}
		if st.State == "fault" && (i == 0 || d.States[i-1].State != "fault") {
			faults++
			if !end.IsZero() {
				repairs = append(repairs, end.Sub(st.T).Minutes())
			}
		}
	}
	if faults >= minFaults && runSec > 0 {
		h, s := healthSum/runSec, speedSum/runSec
		// the sim breaks a worn, fast machine more often: the template's figure is at full health and speed
		factor := (1 + (1-h)*6) * s * s
		p := get("mtbf")
		p.Fitted, p.N = f64(r(runSec/3600*factor/float64(faults), 2)), faults
		p.Quality = map[bool]string{true: "fair", false: "poor"}[faults >= 10]
	} else {
		get("mtbf").N = faults
		get("mtbf").Note = fmt.Sprintf("这段时间只坏过 %d 次，至少要 %d 次", faults, minFaults)
	}
	if len(repairs) >= minFaults {
		lo, hi := quantile(repairs, 0.25), quantile(repairs, 0.75)
		if hi-lo < 0.1 {
			lo, hi = lo*0.8, hi*1.2
		}
		get("repairMin").Fitted, get("repairMin").N = f64(r(math.Max(0.1, lo), 2)), len(repairs)
		get("repairMax").Fitted, get("repairMax").N = f64(r(hi, 2)), len(repairs)
		get("repairMin").Quality = map[bool]string{true: "fair", false: "poor"}[len(repairs) >= 10]
		get("repairMax").Quality = get("repairMin").Quality
	} else {
		for _, k := range []string{"repairMin", "repairMax"} {
			get(k).N = len(repairs)
			get(k).Note = fmt.Sprintf("只修过 %d 次，至少要 %d 次", len(repairs), minFaults)
		}
	}

	// cycle time: the machine's own part counter over its running time
	if len(d.Cycles) >= 2 {
		var parts float64
		for i := 1; i < len(d.Cycles); i++ {
			if dv := d.Cycles[i].V - d.Cycles[i-1].V; dv > 0 {
				parts += dv
			}
		}
		var sec, speed float64
		for i := 1; i < len(d.Cycles); i++ {
			if m, _, known := tl.at(d.Cycles[i-1].T); known && m == run {
				dt := d.Cycles[i].T.Sub(d.Cycles[i-1].T).Seconds()
				sec += dt
				speed += tl.speedAt(d.Cycles[i-1].T) * dt
			}
		}
		if parts >= minCycles && sec > 0 {
			p := get("cycle")
			p.Fitted, p.N = f64(r(sec*(speed/sec)/parts, 2)), int(parts)
			p.Quality = map[bool]string{true: "good", false: "fair"}[parts >= 300]
		} else {
			get("cycle").N = int(parts)
			get("cycle").Note = fmt.Sprintf("运行时只数到 %.0f 件，至少要 %d 件", parts, minCycles)
		}
	} else {
		get("cycle").Note = "设备没有上报件数计数（cycles）"
	}
	return ps, replays
}

// fitThermalAt fits the two rises with the template's time constant.
func fitThermalAt(tl timeline, temps []Sample, tpl Thermal) (Thermal, bool, bool, bool) {
	cs := replayThermal(tl, temps, tpl.Tau)
	var saa, sbb, sab, say, sby float64
	for i, c := range cs {
		if _, _, known := tl.at(temps[i+1].T); !known {
			continue
		}
		y := temps[i+1].V - c.c
		saa += c.a * c.a
		sbb += c.b * c.b
		sab += c.a * c.b
		say += c.a * y
		sby += c.b * y
	}
	th := tpl
	idleOK, runOK := saa > 1, sbb > 1
	switch {
	case idleOK && runOK:
		if det := saa*sbb - sab*sab; math.Abs(det) > 1e-9 {
			th.Idle = (say*sbb - sby*sab) / det
			th.Run = (sby*saa - say*sab) / det
		}
	case runOK:
		th.Run = (sby - th.Idle*sab) / sbb
	case idleOK:
		th.Idle = (say - th.Run*sab) / saa
	}
	return th, idleOK || runOK, idleOK, runOK
}
