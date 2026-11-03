package engine

import "encoding/json"

// Heat is a steel heat a line is using, or one the store holds.
type Heat struct {
	No       string  `json:"no"`
	Mill     string  `json:"mill"`
	Material string  `json:"material,omitempty"`
	Kg       float64 `json:"kg,omitempty"`
}

// LotNow is the heat a line is using and the pieces of it not yet started.
type LotNow struct {
	No       string `json:"no"`
	Supplier string `json:"supplier"`
	Left     int    `json:"left"`
}

// JobNow is the order a line is running.
type JobNow struct {
	No       string `json:"no"`
	Part     string `json:"part"`
	Name     string `json:"name"`
	Heat     string `json:"heat"`
	Supplier string `json:"supplier"`
	Good     int    `json:"good"`
	Qty      int    `json:"qty"`
}

// Press is a machine with a wearing tool (a die): its count of cycles and the tool's wear.
type Press struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Label  string `json:"label"`
	Cycles int64  `json:"cycles"`
	Tool   struct {
		Label string `json:"label"`
		Life  int64  `json:"life"`
		Used  int64  `json:"used"`
	} `json:"tool"`
	Changing bool `json:"changing"`
}

// LineMaterial is a line as the stores see it.
type LineMaterial struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	Product  string  `json:"product"`
	Material string  `json:"material"`
	Input    float64 `json:"input"`
	Lot      *LotNow `json:"lot"`
	Job      *JobNow `json:"job"`
	Presses  []Press `json:"presses"`
}

// PlantMaterial is a plant's lines.
type PlantMaterial struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Lines []LineMaterial `json:"lines"`
}

// Materials are the company's lines with their heats and orders, and its machines with dies (nil
// when it does not run). Reading it changes nothing in the twin.
func (e *Engine) Materials(company string) []PlantMaterial {
	var out []PlantMaterial
	if json.Unmarshal([]byte(e.str("materials", company)), &out) != nil {
		return nil
	}
	return out
}

// MountTool tells the twin a die from the die ledger went on a machine: its tool counts on from
// the die's strokes, against the die's own life.
func (e *Engine) MountTool(company, plant, asset string, used, life int64) bool {
	return e.str("mountTool", company, plant, asset, used, life) == "true"
}

// SetHeats gives the twin the heats the company's store holds, the oldest first: its lines take
// their next lots from them in place of heats it makes up.
func (e *Engine) SetHeats(company string, heats []Heat) bool {
	if heats == nil {
		heats = []Heat{}
	}
	raw, _ := json.Marshal(heats)
	return e.str("setHeats", company, string(raw)) == "true"
}
