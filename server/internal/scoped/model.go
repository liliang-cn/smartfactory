// Package scoped changes one part of a built company — a plant, an area (车间), a line or a
// machine — and nothing else: it resolves the part picked, applies a change to the company's
// config (a form's edited config or the AI's list of changes), refuses any change outside the
// part, and says in plain Chinese what changes, which machine tags (位号) move, and what of the
// rest of the company is left as it was.
//
// The configs it works on are the engine's scope models (Engine.ScopeModel): validated, every
// line's routing written out as its own steps, and with what they lay out to.
package scoped

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// Model is a company config as the engine lays it out for the editor.
type Model struct {
	// Config is the validated config, each line with its steps (objects), $route naming the
	// routing they were copied from.
	Config map[string]any `json:"config"`
	// Routes are the routings of the packs the company uses, by pack and routing, steps as objects.
	Routes map[string]map[string][]any `json:"routes"`
	// Machines are the packs' machine kinds, by pack and key.
	Machines map[string]map[string]MachineKind `json:"machines"`
	Plants   []Plant                           `json:"plants"`
}

// MachineKind is a machine of a pack, as the editor offers it.
type MachineKind struct {
	Label     string  `json:"label"`
	Code      string  `json:"code"`
	Archetype string  `json:"archetype"`
	Cycle     float64 `json:"cycle"`
	Warn      float64 `json:"warn"`
	Trip      float64 `json:"trip"`
	// Run is its body temperature running (°C): the alarm must be at least 6 °C above it
	Run float64 `json:"run"`
}

// Plant is a plant as laid out.
type Plant struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Industry string `json:"industry"`
	Hall     string `json:"hall"`
	Areas    []Area `json:"areas"`
	Lines    []Line `json:"lines"`
}

// Area is an area (车间) of a plant; Index is its place in the plant's areas, -1 when the plant
// lists none (its one area is the hall).
type Area struct {
	Code  string `json:"code"`
	Name  string `json:"name"`
	Note  string `json:"note"`
	Index int    `json:"index"`
}

// Line is a line as laid out: L3, from which entry of the plant's lines (Cfg) and which of its
// repeats (Rep, from 1).
type Line struct {
	ID       string    `json:"id"`
	No       int       `json:"no"`
	Name     string    `json:"name"`
	Cfg      int       `json:"cfg"`
	Rep      int       `json:"rep"`
	Area     string    `json:"area"`
	Speed    float64   `json:"speed"`
	Stations []Station `json:"stations"`
}

// Station is a machine of a line: its tag (ID), the step's machine kind (Key) and what it runs to.
type Station struct {
	ID    string  `json:"id"`
	Key   string  `json:"key"`
	Name  string  `json:"name"`
	Label string  `json:"label"`
	Code  string  `json:"code"`
	Cycle float64 `json:"cycle"`
	Warn  float64 `json:"warn"`
	Trip  float64 `json:"trip"`
}

// Parse reads an engine scope model.
func Parse(raw []byte) (*Model, error) {
	var m Model
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	if m.Config == nil {
		return nil, fmt.Errorf("没有配置")
	}
	return &m, nil
}

// plantCfg is plant pi of the config.
func (m *Model) plantCfg(pi int) map[string]any { return obj(at(m.Config, "plants", pi)) }

// lineCfgs are plant pi's line entries.
func (m *Model) lineCfgs(pi int) []any { return arr(m.plantCfg(pi)["lines"]) }

// laid are the laid-out lines of plant pi from line entry ci, in order of their repeats.
func (m *Model) laid(pi, ci int) []Line {
	var out []Line
	if pi >= len(m.Plants) {
		return nil
	}
	for _, l := range m.Plants[pi].Lines {
		if l.Cfg == ci {
			out = append(out, l)
		}
	}
	return out
}

// lineTitle names line entry ci: 「曲轴 2 线」, or with its repeats 「曲轴 1 线」…「曲轴 3 线」.
func (m *Model) lineTitle(pi, ci int) string {
	ls := m.laid(pi, ci)
	switch len(ls) {
	case 0:
		return "「" + str(obj(at(m.lineCfgs(pi), ci))["name"]) + "」"
	case 1:
		return "「" + ls[0].Name + "」"
	default:
		return "「" + ls[0].Name + "」…「" + ls[len(ls)-1].Name + "」"
	}
}

// station is step si of line entry ci as its first repeat lays it out.
func (m *Model) station(pi, ci, si int) (Station, bool) {
	ls := m.laid(pi, ci)
	if len(ls) == 0 || si >= len(ls[0].Stations) {
		return Station{}, false
	}
	return ls[0].Stations[si], true
}

func (m *Model) industry(pi int) string {
	if pi < len(m.Plants) {
		return m.Plants[pi].Industry
	}
	return ""
}

// ---- JSON helpers ----

func obj(v any) map[string]any {
	o, _ := v.(map[string]any)
	return o
}

func arr(v any) []any {
	a, _ := v.([]any)
	return a
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func num(v any) (float64, bool) {
	f, ok := v.(float64)
	return f, ok
}

// at walks v by keys (strings) and indexes (ints); nil where there is nothing.
func at(v any, path ...any) any {
	for _, p := range path {
		switch k := p.(type) {
		case string:
			v = obj(v)[k]
		case int:
			a := arr(v)
			if k < 0 || k >= len(a) {
				return nil
			}
			v = a[k]
		}
	}
	return v
}

func clone(v any) any {
	b, _ := json.Marshal(v)
	var out any
	json.Unmarshal(b, &out)
	return out
}

func itoa(i int) string { return strconv.Itoa(i) }

// fmtNum writes a number the short way: 45, 0.55.
func fmtNum(f float64) string { return strconv.FormatFloat(f, 'f', -1, 64) }
