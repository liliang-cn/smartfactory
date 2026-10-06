package config

import (
	"encoding/json"
	"os"
	"testing"
)

// The shipped examples must load.
func TestExamples(t *testing.T) {
	c, err := Load("../../examples/smartfactory.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Sources) != 3 || c.Sources[0].Poll.Duration.Seconds() != 2 || c.MQTT.Users["edge"] == "" {
		t.Fatalf("unexpected: %+v", c)
	}
	b, err := os.ReadFile("../../examples/edge.json")
	if err != nil {
		t.Fatal(err)
	}
	var v map[string]any
	if err := json.Unmarshal(b, &v); err != nil {
		t.Fatal(err)
	}
}
