package engine

import "encoding/json"

// Params are a machine's simulation parameters, any subset: a template's, or a calibration's.
type Params struct {
	IdleRise *float64 `json:"idleRise,omitempty"`
	RunRise  *float64 `json:"runRise,omitempty"`
	Tau      *float64 `json:"tau,omitempty"`
	PIdle    *float64 `json:"pIdle,omitempty"`
	PRun     *float64 `json:"pRun,omitempty"`
	Vib      *float64 `json:"vib,omitempty"`
	// Cycle is seconds per unit at 100 % speed.
	Cycle *float64 `json:"cycle,omitempty"`
	// MTBF is running hours between breakdowns at full health.
	MTBF *float64 `json:"mtbf,omitempty"`
	// Repair is minutes a repair takes, [min, max].
	Repair *[2]float64 `json:"repair,omitempty"`
}

// MachineParams is a machine as calibration sees it.
type MachineParams struct {
	Plant     string  `json:"plant"`
	PlantName string  `json:"plantName"`
	Line      string  `json:"line"`
	Asset     string  `json:"asset"`
	Name      string  `json:"name"`
	Label     string  `json:"label"`
	Template  Params  `json:"template"`
	Calib     *Params `json:"calib"`
	// Live: the machine is reporting now.
	Live bool `json:"live"`
}

// MachineParams are the machines' parameters across the company's plants (nil when it does not run).
func (e *Engine) MachineParams(company string) []MachineParams {
	var out []MachineParams
	if json.Unmarshal([]byte(e.str("machineParams", company)), &out) != nil {
		return nil
	}
	return out
}

// Calibrate puts fitted parameters on the company's machines ("plant/asset" → params), the
// templates' back on the others; it says which keys name no machine.
func (e *Engine) Calibrate(company string, c map[string]Params) ([]string, ConfigResult) {
	if c == nil {
		c = map[string]Params{}
	}
	b, _ := json.Marshal(c)
	res := e.verdict(e.str("calibrate", company, string(b)))
	var r struct{ Missing []string }
	json.Unmarshal(res.Detail, &r)
	return r.Missing, res
}

// Residual is what a reporting machine's model got wrong over Seconds: the mean error
// (model − measured), the mean absolute error and the share of the time within tolerance.
type Residual struct {
	Plant   string  `json:"plant"`
	Asset   string  `json:"asset"`
	Field   string  `json:"field"`
	Seconds float64 `json:"seconds"`
	Mean    float64 `json:"mean"`
	MAE     float64 `json:"mae"`
	Within  float64 `json:"within"`
}

// Residuals are what the models got wrong since the last call.
func (e *Engine) Residuals(company string) []Residual {
	var out []Residual
	json.Unmarshal([]byte(e.str("residuals", company)), &out)
	return out
}
