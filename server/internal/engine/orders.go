package engine

import "encoding/json"

// Route is the line an ERP order goes to.
type Route struct {
	Plant     string `json:"plant"`
	PlantName string `json:"plantName"`
	Line      string `json:"line"`
	LineName  string `json:"lineName"`
}

// RouteOrder picks the line for an order: the plant and line it names (id or name), else one
// that makes its part number or product, the one with the least work ahead of it.
func (e *Engine) RouteOrder(company string, order any) (Route, ConfigResult) {
	b, _ := json.Marshal(order)
	res := e.verdict(e.str("routeOrder", company, string(b)))
	var r Route
	if res.OK {
		json.Unmarshal(res.Detail, &r)
	}
	return r, res
}

// OrderEvent is an ERP order a line started or finished.
type OrderEvent struct {
	Kind  string `json:"kind"` // start | done
	No    string `json:"no"`
	Plant string `json:"plant"`
	Line  string `json:"line"`
	Good  int    `json:"good"`
	Scrap int    `json:"scrap"`
	// PlantName and LineName are the names of where it runs.
	PlantName string `json:"plantName"`
	LineName  string `json:"lineName"`
}

// OrderQueue is what the lines are to run: each line's ERP orders in turn ("plant/line" keys),
// the numbers of every order not finished or withdrawn, and what a line with none does: idle
// (waits) or simulate (makes up its own).
type OrderQueue struct {
	Mode   string                `json:"mode"`
	Queue  map[string][]ExtOrder `json:"queue"`
	Active []string              `json:"active"`
}

// ExtOrder is an order as a line runs it; Due is epoch milliseconds.
type ExtOrder struct {
	No       string  `json:"no"`
	PartNo   string  `json:"partNo,omitempty"`
	Name     string  `json:"name,omitempty"`
	Qty      int     `json:"qty"`
	Due      int64   `json:"due,omitempty"`
	Customer string  `json:"customer,omitempty"`
	Price    float64 `json:"price,omitempty"`
}

// OrderSync is what happened to the ERP orders since the last sync.
type OrderSync struct {
	Events  []OrderEvent `json:"events"`
	Running []OrderEvent `json:"running"`
}

// SyncOrders hands the lines their queue (nil leaves it as it is) and collects what started and
// finished since the last call, and how far the running orders are. ok is false when the
// company is not running.
func (e *Engine) SyncOrders(company string, q *OrderQueue) (OrderSync, bool) {
	arg := ""
	if q != nil {
		b, _ := json.Marshal(q)
		arg = string(b)
	}
	js := e.str("syncOrders", company, arg)
	var s OrderSync
	if js == "null" || json.Unmarshal([]byte(js), &s) != nil {
		return s, false
	}
	return s, true
}

// StopOrders has the company's lines make up their own orders again.
func (e *Engine) StopOrders(company string) { e.str("stopOrders", company) }

// FastForward runs a company ahead by seconds as if they had passed (tests).
func (e *Engine) FastForward(company string, seconds float64) {
	e.do(func() {
		if _, err := e.call("fastForward", company, seconds); err != nil {
			e.log.Error("engine call", "fn", "fastForward", "err", err)
		}
	})
}
