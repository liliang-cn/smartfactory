package agents

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"smartfactory/internal/engine"
)

// A machine that has faulted comes first in the live state, is counted as 故障, and is what a
// question about faults finds, even in a plant with dozens of machines running.
func TestMachinesShowFaults(t *testing.T) {
	if _, err := os.Stat("../engine/js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	eng, err := engine.New(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	go eng.Run(ctx)
	time.Sleep(300 * time.Millisecond)

	if r := eng.Command("huaduan", "p1", "RF-101", "fault", nil); !r.OK {
		t.Fatalf("fault: %+v", r)
	}
	time.Sleep(300 * time.Millisecond)

	v, err := machines(eng, "huaduan", "")
	if err != nil {
		t.Fatal(err)
	}
	all := v.(map[string]any)
	first := all["machines"].([]map[string]any)[0]
	if first["id"] != "RF-101" || first["state"] != "fault" || first["state_text"] != "故障" {
		t.Errorf("the faulted machine is not first: %+v", first)
	}
	if n := all["states"].(map[string]int)["故障"]; n < 1 {
		t.Errorf("states: %+v", all["states"])
	}
	if all["total"].(int) < 50 {
		t.Errorf("total = %v, want every machine of the company", all["total"])
	}

	v, err = machines(eng, "huaduan", "现在哪台故障了")
	if err != nil {
		t.Fatal(err)
	}
	found := v.(map[string]any)["machines"].([]map[string]any)
	if len(found) == 0 || found[0]["id"] != "RF-101" {
		t.Errorf("a question about faults finds %+v", found)
	}
	for _, m := range found {
		if m["state"] != "fault" {
			t.Errorf("not faulted but listed: %+v", m)
		}
	}
}
