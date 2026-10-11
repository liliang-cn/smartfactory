package config

import (
	"encoding/json"
	"os"
	"testing"
	"time"
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

// A config written out (an instance's, by `instance init`) reads back the same.
func TestConfigRoundTrip(t *testing.T) {
	c := Config{HTTP: "127.0.0.1:1", Sources: []Source{{Name: "plc", Type: "modbus", Poll: Duration{2 * time.Second}}, {Name: "ua", Type: "opcua"}}}
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var back Config
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("%s: %v", b, err)
	}
	if back.Sources[0].Poll.Duration != 2*time.Second || back.Sources[1].Poll.Duration != 0 {
		t.Errorf("poll: %v %v", back.Sources[0].Poll, back.Sources[1].Poll)
	}
}
