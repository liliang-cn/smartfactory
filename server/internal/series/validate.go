package series

import (
	"fmt"
	"math"
	"strings"
	"sync"
	"time"

	"smartfactory/internal/config"
	"smartfactory/internal/sfconv"
	"smartfactory/internal/tags"
)

// Bounds are what a field can plausibly read: a range, and how fast it can change per second
// (0: any speed).
type Bounds struct {
	Min, Max, Rate float64
}

// Validator flags readings no machine could give: outside the plausible range of the field, or
// a jump faster than it can change. Defaults come from the machine (its trip temperature, its
// running power, its measure's spec); config limits override them.
type Validator struct {
	Limits []config.Limit
	// Machines is a plant's machines as the engine lays them out (nil: no machine defaults).
	Machines func(company, plant string) []sfconv.Machine

	mu    sync.Mutex
	specs map[string]specCache
	prev  map[string]reading
}

type specCache struct {
	at time.Time
	ms map[string]sfconv.Machine
}

type reading struct {
	v      float64
	at     time.Time
	jumped int // flagged jumps in a row: past a few, the new level is real
}

// Check flags t (sets Q) when it is implausible and says why; "" when it is not.
func (v *Validator) Check(t *tags.Tag) string {
	x, ok := number(t.Value)
	if !ok || math.IsNaN(x) || math.IsInf(x, 0) {
		if ok {
			t.Q |= tags.FlagRange
			return fmt.Sprintf("%s %s 不是有效数字", t.Asset, t.Field)
		}
		return ""
	}
	b := v.bounds(t.Company, t.Plant, t.Asset, t.Field)
	key := t.Company + "/" + t.Plant + "/" + t.Asset + "/" + t.Field
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.prev == nil {
		v.prev = map[string]reading{}
	}
	if x < b.Min || x > b.Max {
		t.Q |= tags.FlagRange
		return fmt.Sprintf("%s %s %s 超出 %s～%s", t.Asset, t.Field, num(x), num(b.Min), num(b.Max))
	}
	p, seen := v.prev[key]
	if b.Rate > 0 && seen && t.At.After(p.at) && t.At.Sub(p.at) < time.Minute {
		dt := math.Max(t.At.Sub(p.at).Seconds(), 1)
		if rate := math.Abs(x-p.v) / dt; rate > b.Rate && p.jumped < 3 {
			p.jumped++
			v.prev[key] = p
			t.Q |= tags.FlagSpike
			return fmt.Sprintf("%s %s 从 %s 跳到 %s（每秒 %s，最多 %s）", t.Asset, t.Field, num(p.v), num(x), num(rate), num(b.Rate))
		}
	}
	if !seen || !t.At.Before(p.at) {
		v.prev[key] = reading{v: x, at: t.At}
	}
	return ""
}

func num(x float64) string {
	if math.IsInf(x, 0) {
		return "∞"
	}
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.3f", x), "0"), ".")
}

// bounds are the defaults for a field of a machine, then the matching config limits on top.
func (v *Validator) bounds(company, plant, asset, field string) Bounds {
	b := Bounds{Min: math.Inf(-1), Max: math.Inf(1)}
	m, known := v.machine(company, plant, asset)
	switch field {
	case "temp":
		b = Bounds{Min: -50, Max: 2000, Rate: 50}
		if known && m.Trip > 0 {
			b.Max = m.Trip + 200
		}
	case "power":
		b = Bounds{Min: -1, Max: math.Inf(1)}
		if known && m.Power > 0 {
			b.Max = m.Power*5 + 10
		}
	case "vib":
		b = Bounds{Min: 0, Max: 100}
		if known && m.Vib > 0 {
			b.Max = math.Max(100, m.Vib*20)
		}
	case "health":
		b = Bounds{Min: 0, Max: 1}
	case "speed":
		b = Bounds{Min: 0, Max: 150}
	case "battery":
		b = Bounds{Min: 0, Max: 100}
	case "toolUsed", "toolLife", "count", "stock", "mcount":
		b = Bounds{Min: 0, Max: math.Inf(1)}
	case "measure", "lastMeasure":
		if known && m.Measure != nil {
			ms := m.Measure
			w := 100 * ms.Sigma
			if ms.Lo != nil && ms.Hi != nil {
				w = math.Max(w, 10*(*ms.Hi-*ms.Lo))
			}
			b = Bounds{Min: ms.Target - w, Max: ms.Target + w}
		}
	}
	for _, l := range v.Limits {
		if !match(l.Company, company) || !match(l.Plant, plant) || !match(l.Asset, asset) || !match(l.Field, field) {
			continue
		}
		if l.Min != nil {
			b.Min = *l.Min
		}
		if l.Max != nil {
			b.Max = *l.Max
		}
		if l.Rate != nil {
			b.Rate = *l.Rate
		}
	}
	return b
}

func match(pattern, s string) bool {
	switch {
	case pattern == "" || pattern == "*":
		return true
	case strings.HasSuffix(pattern, "*"):
		return strings.HasPrefix(s, strings.TrimSuffix(pattern, "*"))
	}
	return pattern == s
}

// machine is an asset's spec, refreshed once a minute (a plant's layout can change).
func (v *Validator) machine(company, plant, asset string) (sfconv.Machine, bool) {
	if v.Machines == nil {
		return sfconv.Machine{}, false
	}
	k := company + "/" + plant
	v.mu.Lock()
	c, ok := v.specs[k]
	v.mu.Unlock()
	if !ok || time.Since(c.at) > time.Minute {
		c = specCache{at: time.Now(), ms: map[string]sfconv.Machine{}}
		for _, m := range v.Machines(company, plant) {
			c.ms[m.ID] = m
		}
		v.mu.Lock()
		if v.specs == nil {
			v.specs = map[string]specCache{}
		}
		v.specs[k] = c
		v.mu.Unlock()
	}
	m, ok := c.ms[asset]
	return m, ok
}
