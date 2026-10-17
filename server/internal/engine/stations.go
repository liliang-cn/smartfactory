package engine

import "encoding/json"

// Spec is a machine's quality measure: what each unit it makes is measured for, and the limits.
type Spec struct {
	Label  string   `json:"label"`
	Unit   string   `json:"unit"`
	Target float64  `json:"target"`
	Lo     *float64 `json:"lo,omitempty"`
	Hi     *float64 `json:"hi,omitempty"`
	Digits int      `json:"digits"`
}

// Station is one place on a line, as the forms people fill in address it.
type Station struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Label   string `json:"label"`
	Measure *Spec  `json:"measure,omitempty"`
}

// LineStations is a line and its stations in order.
type LineStations struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Stations []Station `json:"stations"`
}

// PlantStations is a plant's lines.
type PlantStations struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Lines []LineStations `json:"lines"`
}

// Stations are the company's plants, lines and stations (nil when it does not run).
func (e *Engine) Stations(company string) []PlantStations {
	var out []PlantStations
	if json.Unmarshal([]byte(e.str("stations", company)), &out) != nil {
		return nil
	}
	return out
}

// Alert raises a quality alarm (code) on a machine from what a person wrote down, or clears it
// when text is empty; false when there is no such machine.
func (e *Engine) Alert(company, plant, asset, code, text string) bool {
	return e.str("alert", company, plant, asset, code, text) == "true"
}

// Readings are the twin's simulated values of fields of machines ({plant, asset, field} each), in
// order: nil for a machine whose equipment reports its own, or a field the twin does not model.
func (e *Engine) Readings(company string, keys [][3]string) []*float64 {
	raw, _ := json.Marshal(keys)
	var out []*float64
	if json.Unmarshal([]byte(e.str("readings", company, string(raw))), &out) != nil || len(out) != len(keys) {
		return make([]*float64, len(keys))
	}
	return out
}
