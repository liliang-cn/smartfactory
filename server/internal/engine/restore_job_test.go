package engine

import (
	"encoding/json"
	"testing"
	"time"
)

// A company changed under a running plant (a line given another product, so other part numbers)
// carries on from the state saved before the change: a saved order may name a part number the
// line no longer has. The plant must not trip over it when the order is done and the next is made.
func TestRestoreStaleOrders(t *testing.T) {
	if testing.Short() {
		t.Skip("half a day of plant time")
	}
	e := offline(t, "huaduan")
	run(t, e, "huaduan", 30*time.Minute, 1)
	var saved map[string]any
	if err := json.Unmarshal([]byte(e.SaveJSON("huaduan")), &saved); err != nil {
		t.Fatal(err)
	}
	stale := 0
	for _, p := range saved["plants"].(map[string]any) {
		for _, l := range p.(map[string]any)["lines"].([]any) {
			line := l.(map[string]any)
			for _, k := range []string{"job", "last"} {
				if j, ok := line[k].(map[string]any); ok {
					// another product's order: a part number and a place in the list this line does not have
					j["variant"], j["partNo"] = 9, "GONE-001"
					stale++
				}
			}
		}
	}
	if stale == 0 {
		t.Fatal("no order on any line to make stale")
	}
	raw, _ := json.Marshal(saved)
	f := offline(t, "huaduan")
	f.RestoreJSON("huaduan", string(raw))
	// long enough for every order to finish and the next to be made
	if !f.Advance("huaduan", 12*3600, 10) {
		t.Fatal("the plant stopped on a saved order of a part number the line no longer has")
	}
}
