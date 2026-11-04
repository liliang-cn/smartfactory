// Package plan is production planning and scheduling (生产计划): the open orders laid out on the
// lines with finite capacity — each line's real rate and working calendar, changeovers between
// part numbers, material that has to be there, dies that cannot be on two lines at once — so
// that each order gets a line, a start and an end, and the lateness it will have and why.
//
// The scheduler is a heuristic, deterministic and fast (hundreds of orders on dozens of lines
// well under a second): due-date dispatching with changeover-aware batching of the same part,
// then a local search that moves and swaps orders to cut lateness and changeovers. Locked
// orders keep their line and start. Plans are drafted, compared with the one in force and
// published; publishing hands each order its line and place in the queue the lines run.
package plan

import "time"

// Goal is what a plan weighs most.
type Goal string

const (
	// Due puts delivery dates first: changeovers are cheap next to a late order.
	Due Goal = "due"
	// Balance weighs lateness and changeovers about evenly.
	Balance Goal = "balance"
	// Setups batches the same part together even if an order or two is a little late.
	Setups Goal = "setup"
)

// GoalLabel is how a goal reads.
var GoalLabel = map[Goal]string{Due: "交期优先", Balance: "平衡", Setups: "少换型"}

// Material policies: an order short of material is held back (not planned) or planned all the
// same at its shortage.
const (
	Hold = "hold"
	AsIs = "plan"
)

// Where an order's expected material arrival comes from.
const (
	Manual   = "manual"
	Purchase = "purchase"
	None     = "none"
)

// Line is a line as the scheduler sees it.
type Line struct {
	// Key is "plant/line".
	Key       string `json:"key"`
	Plant     string `json:"plant"`
	PlantName string `json:"plantName"`
	ID        string `json:"id"`
	Name      string `json:"name"`
	No        int    `json:"no"`
	// Rate is seconds per good piece: the bottleneck's cycle (Neck) over the efficiency it runs at
	// (Eff), which comes from RateSource.
	Rate       float64 `json:"rate"`
	Neck       float64 `json:"neck"`
	Eff        float64 `json:"eff"`
	RateSource string  `json:"rateSource"`
	// Setup is the seconds a changeover to another part number takes, from SetupSource.
	Setup       float64 `json:"setup"`
	SetupSource string  `json:"setupSource"`
	// Kinds are the kinds of die its machines take (锻模, 切边模 …).
	Kinds []string `json:"kinds,omitempty"`
	// Last is the part it ran last (the first order of another part needs a changeover).
	Last string `json:"last,omitempty"`
	// Running is the ERP order it runs now, if any: it carries on first.
	Running *Running `json:"running,omitempty"`
	// Down says what stops it now (a long fault, a machine stopped), "" when it runs.
	Down string `json:"down,omitempty"`
	// Crew: it stops for meal breaks and slows at handovers.
	Crew bool      `json:"crew"`
	Cal  *Calendar `json:"-"`
	// Product is the product family it makes, Family its key and name (dies are booked to them).
	Product string   `json:"product"`
	Family  []string `json:"-"`
}

// Running is the order a line runs now: what is left of it.
type Running struct {
	No       string    `json:"no"`
	Part     string    `json:"part"`
	Name     string    `json:"name,omitempty"`
	Customer string    `json:"customer,omitempty"`
	Left     int       `json:"left"`
	Due      time.Time `json:"due,omitzero"`
}

// Order is an open order to plan.
type Order struct {
	No       string `json:"no"`
	Part     string `json:"part"`
	Name     string `json:"name,omitempty"`
	Customer string `json:"customer,omitempty"`
	Qty      int    `json:"qty"`
	// Due is the delivery date (zero: none); Created when it came.
	Due     time.Time `json:"due"`
	Created time.Time `json:"created"`
	// Lines are the keys of the lines that can make it, best first.
	Lines []string `json:"lines"`
	// Release is the earliest it can start (its material comes then); zero: now.
	Release time.Time `json:"release"`
	// Short is what material it lacks ("" when it is all there or there is no BOM to tell).
	Short string `json:"short,omitempty"`
	// Hold: short of material with nothing known of when it comes, and held back.
	Hold bool `json:"hold,omitempty"`
	// Arrive is where Release comes from (nil: the order lacks nothing).
	Arrive *Arrive `json:"arrive,omitempty"`
	// Lock keeps it on a line from a start: the scheduler does not move it.
	Lock *Lock `json:"lock,omitempty"`
	// Family are the names of the product it is a part of (dies are booked to a part or its product).
	Family []string `json:"-"`
}

// Arrive says when the material an order lacks is expected and on whose word: a person's
// (manual, which overrides everything), the purchase lines the stores keep (purchase), or nobody's
// (none: Text says why it cannot be told).
type Arrive struct {
	At   time.Time `json:"at,omitzero"`
	From string    `json:"from"`
	Text string    `json:"text,omitempty"`
}

// Lock pins an order to a line from a start (the start of its changeover, if it has one).
type Lock struct {
	Line  string    `json:"line"`
	Start time.Time `json:"start"`
}

// Die is a die (a set of dies for one operation) the stores keep.
type Die struct {
	No      string `json:"no"`
	Kind    string `json:"kind"`
	Product string `json:"product"`
	Life    int64  `json:"life"`
	Left    int64  `json:"left"`
	// Line is the line it is mounted on ("" in store).
	Line string `json:"line,omitempty"`
}

// Input is everything a plan is made from.
type Input struct {
	Now    time.Time `json:"now"`
	Lines  []Line    `json:"lines"`
	Orders []Order   `json:"orders"`
	Dies   []Die     `json:"dies,omitempty"`
	Stops  []Stop    `json:"stops,omitempty"`
	Goal   Goal      `json:"goal"`
	// Days is how far ahead the calendar is laid out (default 90).
	Days int `json:"days,omitempty"`
}

// Why is one reason an order is late, or a flag on it.
type Why struct {
	// Kind: capacity | material | die | due | setup | down
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// Planned is an order as the plan has it.
type Planned struct {
	No       string    `json:"no"`
	Part     string    `json:"part"`
	Name     string    `json:"name,omitempty"`
	Customer string    `json:"customer,omitempty"`
	Qty      int       `json:"qty"`
	Due      time.Time `json:"due,omitzero"`
	// Line is "plant/line"; Seq its place on the line (1 first).
	Line     string `json:"line"`
	LineName string `json:"lineName"`
	Seq      int    `json:"seq"`
	// SetupStart is when its changeover starts (= Start without one), Start production, End the last piece.
	SetupStart time.Time `json:"setupStart"`
	Start      time.Time `json:"start"`
	End        time.Time `json:"end"`
	SetupMin   float64   `json:"setupMin"`
	// LateMin is how many minutes after its due date it ends (0 on time).
	LateMin float64 `json:"lateMin"`
	Late    bool    `json:"late"`
	// Why it is late; Flags what to watch (缺料, 模具).
	Why   []Why `json:"why,omitempty"`
	Flags []Why `json:"flags,omitempty"`
	// Arrive: when its missing material is expected, and where that comes from.
	Arrive *Arrive  `json:"arrive,omitempty"`
	Dies   []string `json:"dies,omitempty"`
	// Running: the line runs it now; Locked: kept where it is; Held: short of material, not planned.
	Running bool `json:"running,omitempty"`
	Locked  bool `json:"locked,omitempty"`
	Held    bool `json:"held,omitempty"`
	// Lines it could also run on.
	Lines []string `json:"lines,omitempty"`
}

// LineKPI is how loaded a line is in the plan.
type LineKPI struct {
	Key        string    `json:"key"`
	Name       string    `json:"name"`
	PlantName  string    `json:"plantName"`
	Orders     int       `json:"orders"`
	Util       float64   `json:"util"`
	BusyUntil  time.Time `json:"busyUntil,omitzero"`
	Setups     int       `json:"setups"`
	SetupHours float64   `json:"setupHours"`
	Rate       float64   `json:"rate"`
	RateSource string    `json:"rateSource"`
	Setup      float64   `json:"setupMin"`
	SetupSrc   string    `json:"setupSource"`
	Down       string    `json:"down,omitempty"`
}

// KPI is a plan in figures.
type KPI struct {
	Planned    int     `json:"planned"`
	Held       int     `json:"held"`
	OnTime     int     `json:"onTime"`
	OnTimePct  float64 `json:"onTimePct"`
	Late       int     `json:"late"`
	LateHours  float64 `json:"lateHours"`
	Setups     int     `json:"setups"`
	SetupHours float64 `json:"setupHours"`
	// Util is the mean utilisation of the lines with work; End when the last order ends.
	Util float64   `json:"util"`
	End  time.Time `json:"end,omitzero"`
	Cost float64   `json:"cost"`
}

// Result is a plan: every order with its line and times, held ones apart, and the figures.
type Result struct {
	Goal   Goal      `json:"goal"`
	Now    time.Time `json:"now"`
	Orders []Planned `json:"orders"`
	Held   []Planned `json:"held"`
	Lines  []LineKPI `json:"lines"`
	KPI    KPI       `json:"kpi"`
	// Unplaceable are orders no line makes.
	Unplaceable []Planned `json:"unplaceable,omitempty"`
	// Evals is how many sequences the search tried; Millis how long it took.
	Evals  int   `json:"evals"`
	Millis int64 `json:"millis"`
}

// Seq is a plan's order of work: each line's orders in turn ("plant/line" → numbers).
type Seq map[string][]string

// Seq is the result's order of work.
func (r *Result) Seq() Seq {
	s := Seq{}
	for _, o := range r.Orders {
		s[o.Line] = append(s[o.Line], o.No)
	}
	return s
}
