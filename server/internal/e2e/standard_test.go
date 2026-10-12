package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"

	"smartfactory/internal/adapters"
	"smartfactory/internal/config"
	"smartfactory/internal/devices"
	"smartfactory/internal/engine"
	"smartfactory/internal/mapping"
	"smartfactory/internal/sfptest"
	"smartfactory/internal/tags"
)

// Everything on the wire of the MQTT convention conforms to the standard: what equipment
// publishes (here the device emulator, for a plant with vehicles, measured units and inventory)
// and the commands the server publishes back. Measured units come every few seconds to minutes,
// so the run checks those it happens to see.
func TestDeviceConformance(t *testing.T) {
	if _, err := os.Stat("../engine/js/engine.js"); err != nil {
		t.Skip("engine bundle not built")
	}
	std := sfptest.Load(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	port := freePort(t)
	url := fmt.Sprintf("tcp://127.0.0.1:%d", port)
	hub := tags.NewHub()
	broker, err := adapters.StartBroker(config.Broker{Listen: fmt.Sprintf("127.0.0.1:%d", port), Prefix: "sf", Public: true}, hub, nil, log)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()

	var mu sync.Mutex
	seen := map[string]int{} // kind of message → count
	tap := paho.NewClient(paho.NewClientOptions().AddBroker(url).SetClientID("tap"))
	if tok := tap.Connect(); tok.Wait() && tok.Error() != nil {
		t.Fatal(tok.Error())
	}
	defer tap.Disconnect(100)
	tap.Subscribe("sf/#", 1, func(_ paho.Client, m paho.Message) {
		parts := strings.Split(m.Topic(), "/")
		mu.Lock()
		defer mu.Unlock()
		switch {
		case len(parts) == 4:
			std.Check("DeviceFields", m.Payload())
			seen["object"]++
		case len(parts) == 5 && parts[4] == "cmd":
			std.Check("DeviceCommand", m.Payload())
			seen["cmd"]++
		case len(parts) == 5:
			// one field as plain text: the same field, as it would sit in an object
			v, err := mapping.Extract(m.Payload(), "")
			if err != nil {
				t.Errorf("%s: %v", m.Topic(), err)
				return
			}
			b, _ := json.Marshal(map[string]any{parts[4]: v})
			std.Check("DeviceFields", b)
			seen["field:"+parts[4]]++
		default:
			t.Errorf("topic outside the convention: %s", m.Topic())
		}
	}).Wait()

	devEng, err := engine.New(log)
	if err != nil {
		t.Fatal(err)
	}
	cfg := devices.Config{Every: config.Duration{Duration: 300 * time.Millisecond}, Plants: []devices.Plant{
		{Company: "huaduan", Plant: "p1", Protocol: "mqtt", Broker: url},
	}}
	go devices.Run(ctx, cfg, devEng, log)

	deadline := time.Now().Add(30 * time.Second)
	for {
		mu.Lock()
		ok := seen["object"] > 100
		mu.Unlock()
		if ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not enough traffic: %v", seen)
		}
		time.Sleep(200 * time.Millisecond)
	}
	arg := 1.2
	hub.Command(tags.Command{Company: "huaduan", Plant: "p1", Asset: "HP-101", Cmd: "speed", Arg: &arg, By: "operator"})
	hub.Command(tags.Command{Company: "huaduan", Plant: "p1", Asset: "HP-101", Cmd: "stop", By: "mcp"})
	for {
		mu.Lock()
		n := seen["cmd"]
		mu.Unlock()
		if n >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("commands not seen on the wire: %v", seen)
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Logf("checked %v", seen)
}
