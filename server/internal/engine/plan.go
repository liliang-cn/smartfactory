package engine

import (
	"encoding/json"
	"fmt"
)

// PlanCalendar is a plant's working week as the industry pack sets it.
type PlanCalendar struct {
	PM *struct {
		Hours float64 `json:"hours"`
		Start float64 `json:"start"`
	} `json:"pm,omitempty"`
	Open *struct {
		From float64 `json:"from"`
		To   float64 `json:"to"`
	} `json:"open,omitempty"`
	Shifts *struct {
		At       []float64 `json:"at"`
		Handover float64   `json:"handover"`
		Pace     float64   `json:"pace"`
		Names    []string  `json:"names,omitempty"`
	} `json:"shifts,omitempty"`
	Breaks []struct {
		At      float64 `json:"at"`
		Minutes float64 `json:"minutes"`
		Label   string  `json:"label"`
	} `json:"breaks,omitempty"`
	// Off are the weekdays the plant does not run (Sunday 0).
	Off []int `json:"off,omitempty"`
}

// PlanPart is a part number a line can run.
type PlanPart struct {
	PartNo string `json:"partNo"`
	Name   string `json:"name"`
}

// PlanJob is the ERP order a line runs now.
type PlanJob struct {
	No   string `json:"no"`
	Part string `json:"part"`
	Qty  int    `json:"qty"`
	Good int    `json:"good"`
}

// PlanPress is a machine of a line with a wearing tool (a die) of a kind.
type PlanPress struct {
	ID   string `json:"id"`
	Kind string `json:"kind"`
}

// PlanLine is what a plan needs to know of a line: what it makes, how fast, how long a
// changeover takes, and what it runs now.
type PlanLine struct {
	ID          string     `json:"id"`
	No          int        `json:"no"`
	Name        string     `json:"name"`
	Product     string     `json:"product"`
	ProductName string     `json:"productName"`
	Parts       []PlanPart `json:"parts"`
	// Neck is the bottleneck's cycle in seconds; Perf and Quality the line's measured performance
	// and yield over its counting window of Span seconds; PlanEff the efficiency its recent orders ran at.
	Neck    float64 `json:"neck"`
	Perf    float64 `json:"perf"`
	Quality float64 `json:"quality"`
	Span    float64 `json:"span"`
	PlanEff float64 `json:"planEff"`
	// Changeover is the configured minutes (nil: none configured); ToolChange the seconds the
	// slowest tool change of its machines takes.
	Changeover *float64    `json:"changeover"`
	ToolChange float64     `json:"toolChange"`
	Tools      []string    `json:"tools"`
	Presses    []PlanPress `json:"presses"`
	Crew       bool        `json:"crew"`
	Running    *PlanJob    `json:"running"`
	Last       string      `json:"last"`
	Down       string      `json:"down"`
}

// PlanPlant is a plant's lines and calendar.
type PlanPlant struct {
	ID       string       `json:"id"`
	Name     string       `json:"name"`
	Unit     string       `json:"unit"`
	Calendar PlanCalendar `json:"calendar"`
	Lines    []PlanLine   `json:"lines"`
}

// PlanModel is a company's lines as a plan sees them, and the plants' clock (epoch ms).
type PlanModel struct {
	Now    int64       `json:"now"`
	Plants []PlanPlant `json:"plants"`
}

// PlanModel is what a production plan needs to know of the company's lines; nil when it does not run.
func (e *Engine) PlanModel(company string) *PlanModel {
	js := e.str("planModel", company)
	var m PlanModel
	if js == "null" || json.Unmarshal([]byte(js), &m) != nil {
		return nil
	}
	return &m
}

// PlanEvent is an order starting or finishing in a plan run (At in epoch ms); kind running is
// one the line was already running when the run began.
type PlanEvent struct {
	Kind  string `json:"kind"`
	No    string `json:"no"`
	Plant string `json:"plant"`
	Line  string `json:"line"`
	At    int64  `json:"at"`
	Good  int    `json:"good"`
	Scrap int    `json:"scrap"`
}

// PlanRunResult is what a plan run did: from when to when, what started and finished, and the
// orders still running at the end.
type PlanRunResult struct {
	Start  int64       `json:"start"`
	End    int64       `json:"end"`
	Hours  float64     `json:"hours"`
	Events []PlanEvent `json:"events"`
	Left   []struct {
		No    string `json:"no"`
		Plant string `json:"plant"`
		Line  string `json:"line"`
		Good  int    `json:"good"`
		Qty   int    `json:"qty"`
	} `json:"left"`
}

// PlanRun runs a plan ({"plant/line": orders in turn}) on this engine's copy of the company for
// up to hours, in steps of dt seconds. Meant for an offline engine restored from the live state.
func (e *Engine) PlanRun(company string, queue map[string][]ExtOrder, hours, dt float64) (*PlanRunResult, error) {
	return e.PlanRunFresh(company, queue, hours, dt, false)
}

// PlanRunFresh is PlanRun; with fresh the lines let go of the ERP order they run now.
func (e *Engine) PlanRunFresh(company string, queue map[string][]ExtOrder, hours, dt float64, fresh bool) (*PlanRunResult, error) {
	b, _ := json.Marshal(queue)
	return ParsePlanRun(e.str("planRun", company, string(b), hours, dt, fresh))
}

// ParsePlanRun reads what planRun answered (here or in node).
func ParsePlanRun(js string) (*PlanRunResult, error) {
	var out struct {
		OK     bool     `json:"ok"`
		Errors []string `json:"errors"`
		PlanRunResult
	}
	if err := json.Unmarshal([]byte(js), &out); err != nil {
		return nil, err
	}
	if !out.OK {
		return nil, fmt.Errorf("%v", out.Errors)
	}
	return &out.PlanRunResult, nil
}
