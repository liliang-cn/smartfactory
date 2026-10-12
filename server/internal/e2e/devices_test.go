package e2e

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"smartfactory/internal/adapters"
	"smartfactory/internal/config"
	"smartfactory/internal/devices"
	"smartfactory/internal/engine"
	"smartfactory/internal/sfconv"
	"smartfactory/internal/tags"
)

// Three plants of emulated equipment, one per protocol, read by the twin's adapters through the
// SmartFactory conventions; a command travels back over each protocol to the equipment.
func TestDeviceFleet(t *testing.T) {
	if _, err := os.Stat("../engine/js/engine.js"); err != nil {
		t.Skip("engine bundle not built")
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if os.Getenv("E2E_LOG") != "" {
		log = slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	mqttPort, modbusPort, opcPort := freePort(t), freePort(t), freePort(t)
	hub := tags.NewHub()
	broker, err := adapters.StartBroker(config.Broker{Listen: fmt.Sprintf("127.0.0.1:%d", mqttPort), Prefix: "sf", Public: true}, hub, nil, log)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()

	devEng, err := engine.New(log)
	if err != nil {
		t.Fatal(err)
	}
	cfg := devices.Config{Every: config.Duration{Duration: 300 * time.Millisecond}, Plants: []devices.Plant{
		{Company: "hetian", Plant: "bakery", Protocol: "mqtt", Broker: fmt.Sprintf("tcp://127.0.0.1:%d", mqttPort)},
		{Company: "ruineng", Plant: "cell", Protocol: "modbus", Listen: fmt.Sprintf("127.0.0.1:%d", modbusPort)},
		{Company: "yuanlan", Plant: "paint", Protocol: "opcua", Host: "127.0.0.1", Port: opcPort},
	}}
	go func() {
		if err := devices.Run(ctx, cfg, devEng, log); err != nil {
			t.Errorf("devices: %v", err)
		}
	}()

	// The twin's own engine only to learn the machine lists (same config on both ends).
	twin, err := engine.New(log)
	if err != nil {
		t.Fatal(err)
	}
	go twin.Run(ctx)
	time.Sleep(time.Second)
	for _, src := range []config.Source{
		{Name: "cell-plc", Type: "modbus", URL: fmt.Sprintf("tcp://127.0.0.1:%d", modbusPort), Convention: "sf", Company: "ruineng", Plant: "cell", Public: true, Poll: config.Duration{Duration: 300 * time.Millisecond}},
		{Name: "paint-scada", Type: "opcua", URL: fmt.Sprintf("opc.tcp://127.0.0.1:%d", opcPort), Convention: "sf", Company: "yuanlan", Plant: "paint", Public: true, Poll: config.Duration{Duration: 300 * time.Millisecond}},
	} {
		src = sfconv.Expand(src, twin.Machines(src.Company, src.Plant), twin.Fleet(src.Company, src.Plant))
		if src.Type == "modbus" {
			go adapters.RunModbus(ctx, src, hub, log)
		} else {
			go adapters.RunOPCUA(ctx, src, hub, log)
		}
	}

	type plant struct{ c, p, src string }
	plants := []plant{{"hetian", "bakery", "dev-hetian-bakery"}, {"ruineng", "cell", "cell-plc"}, {"yuanlan", "paint", "paint-scada"}}
	deadline := time.Now().Add(20 * time.Second)
	for _, pl := range plants {
		want := len(twin.Machines(pl.c, pl.p))
		for {
			fed := map[string]bool{}
			states := 0
			for _, tg := range hub.Last(pl.c, pl.p, "") {
				if tg.Source == pl.src && tg.Field == "temp" {
					fed[tg.Asset] = true
				}
				if tg.Field == "state" {
					states++
				}
			}
			if len(fed) == want && states == want {
				t.Logf("%s/%s: %d machines over %s", pl.c, pl.p, want, pl.src)
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s/%s: %d/%d machines fed, %d states; sources %+v", pl.c, pl.p, len(fed), want, states, hub.Sources())
			}
			time.Sleep(200 * time.Millisecond)
		}
	}

	// Vehicle positions arrive over every protocol too.
	for _, pl := range plants {
		veh := twin.Fleet(pl.c, pl.p).Vehicles
		if len(veh) == 0 {
			t.Fatalf("%s/%s has no vehicles", pl.c, pl.p)
		}
		for {
			n := 0
			for _, tg := range hub.Last(pl.c, pl.p, veh[0].ID) {
				if tg.Field == "x" || tg.Field == "z" {
					n++
				}
			}
			if n == 2 {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s/%s: no position for %s", pl.c, pl.p, veh[0].ID)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}

	// A visitor's stop reaches emulated equipment over each protocol and comes back as its state.
	for _, pl := range plants {
		asset := twin.Machines(pl.c, pl.p)[2].ID
		hub.Command(tags.Command{Company: pl.c, Plant: pl.p, Asset: asset, Cmd: "stop", By: "visitor"})
		for {
			got := ""
			for _, tg := range hub.Last(pl.c, pl.p, asset) {
				if tg.Field == "state" {
					got, _ = tg.Value.(string)
				}
			}
			if got == "stopped" {
				t.Logf("%s/%s %s stopped over %s", pl.c, pl.p, asset, pl.src)
				break
			}
			if time.Now().After(deadline.Add(10 * time.Second)) {
				t.Fatalf("%s/%s %s: state %q after stop", pl.c, pl.p, asset, got)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
}
