package engine

import (
	"encoding/json"
	"time"
)

// optionalFns are engine functions an older bundle may lack; their wrappers answer empty then.
var optionalFns = []string{"tables", "reportTable", "tableSessions"}

// Table is a restaurant table of a plant as the twin has it now.
type Table struct {
	Plant     string `json:"plant"`
	PlantName string `json:"plantName"`
	Line      string `json:"line"`
	LineName  string `json:"lineName"`
	ID        string `json:"id"`
	Area      string `json:"area"`
	Seats     int    `json:"seats"`
	// State is free, occupied, reserved or cleaning.
	State  string `json:"state"`
	People int    `json:"people"`
	// Since is when the state began (ms).
	Since int64 `json:"since"`
	// Source is "sim" or who reported it; Reported while reports govern the table.
	Source   string `json:"source"`
	Reported bool   `json:"reported"`
}

// SimSession is a simulated dining session the engine opened (End nil) or closed.
type SimSession struct {
	Plant  string `json:"plant"`
	Table  string `json:"table"`
	Start  int64  `json:"start"`
	End    *int64 `json:"end"`
	People int    `json:"people"`
	Source string `json:"source"`
}

// HasTables reports whether the engine bundle knows restaurant tables.
func (e *Engine) HasTables() bool {
	_, ok := e.fn["tables"]
	return ok
}

// Tables is every table of every plant of the company and its state now (JSON array), "[]"
// when the company has none or the engine does not know tables.
func (e *Engine) Tables(company string) string {
	if !e.HasTables() {
		return "[]"
	}
	if s := e.str("tables", company); s != "null" && s != "" {
		return s
	}
	return "[]"
}

// TableList is Tables decoded.
func (e *Engine) TableList(company string) []Table {
	var out []Table
	if err := json.Unmarshal([]byte(e.Tables(company)), &out); err != nil {
		return nil
	}
	return out
}

// ReportTable applies a reported table state to the twin (older than the table's current
// reported state: ignored). False for a table the company does not have.
func (e *Engine) ReportTable(company, plant, table, state string, people int, since time.Time, source string) bool {
	if _, ok := e.fn["reportTable"]; !ok {
		return false
	}
	return e.str("reportTable", company, plant, table, state, float64(people), float64(since.UnixMilli()), source) == "true"
}

// TableSessions drains the simulated sessions opened or closed since the last call.
func (e *Engine) TableSessions(company string) []SimSession {
	if _, ok := e.fn["tableSessions"]; !ok {
		return nil
	}
	var out []SimSession
	if err := json.Unmarshal([]byte(e.str("tableSessions", company)), &out); err != nil {
		return nil
	}
	return out
}
