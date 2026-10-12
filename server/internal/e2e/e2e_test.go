// End to end: a fake node_exporter → the edge gateway (MQTT publisher, Modbus TCP slave,
// OPC UA server) → the server's broker and adapters → the tag hub, and a command back out.
package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"smartfactory/internal/adapters"
	"smartfactory/internal/config"
	"smartfactory/internal/edge"
	"smartfactory/internal/tags"
)

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestProtocolsEndToEnd(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if os.Getenv("E2E_LOG") != "" {
		log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	exporter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `node_hwmon_temp_celsius{chip="platform_coretemp_0",sensor="temp1"} 64
node_hwmon_temp_celsius{chip="platform_coretemp_1",sensor="temp1"} 67
node_hwmon_temp_celsius{chip="nvme_nvme0",sensor="temp1"} 54.85
`)
	}))
	defer exporter.Close()

	mqttPort, modbusPort, opcPort := freePort(t), freePort(t), freePort(t)
	hub := tags.NewHub()
	broker, err := adapters.StartBroker(config.Broker{Listen: fmt.Sprintf("127.0.0.1:%d", mqttPort), Prefix: "sf"}, hub, nil, log)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()

	ecfg := edge.Config{
		Scrape: []string{exporter.URL},
		Every:  config.Duration{Duration: 200 * time.Millisecond},
		Metrics: map[string]edge.Selector{
			"cpu0": {Name: "node_hwmon_temp_celsius", Labels: map[string]string{"chip": "platform_coretemp_0", "sensor": "temp1"}},
			"cpu1": {Name: "node_hwmon_temp_celsius", Labels: map[string]string{"chip": "platform_coretemp_1", "sensor": "temp1"}},
			"nvme": {Name: "node_hwmon_temp_celsius", Labels: map[string]string{"chip": "nvme_nvme0"}},
		},
	}
	jsonCfg := fmt.Sprintf(`{
		"mqtt": {"url": "tcp://127.0.0.1:%d", "publish": [{"metric": "cpu0", "topic": "sf/huaduan/p1/HP-101/temp"}], "buffer": %q},
		"modbus": {"listen": "127.0.0.1:%d", "holding": [{"metric": "cpu1", "address": 10, "scale": 10}]},
		"opcua": {"host": "127.0.0.1", "port": %d, "nodes": [{"metric": "nvme", "node": "dell.nvme.temp"}]}
	}`, mqttPort, t.TempDir()+"/buffer.jsonl", modbusPort, opcPort)
	if err := jsonUnmarshal(jsonCfg, &ecfg); err != nil {
		t.Fatal(err)
	}
	go func() {
		if err := edge.Run(ctx, ecfg, log); err != nil {
			t.Errorf("edge: %v", err)
		}
	}()
	time.Sleep(500 * time.Millisecond)

	scale := 0.1
	go adapters.RunModbus(ctx, config.Source{
		Name: "plc", Type: "modbus", URL: fmt.Sprintf("tcp://127.0.0.1:%d", modbusPort), Poll: config.Duration{Duration: 200 * time.Millisecond},
		Points:   []config.Point{{Register: "holding", Address: 10, Company: "huaduan", Plant: "p1", Asset: "HP-201", Field: "temp", Scale: &scale}},
		Commands: []config.CmdBinding{{Asset: "HP-201", Cmd: "stop", Register: "coil", Address: 3, Value: 1}},
	}, hub, log)
	go adapters.RunOPCUA(ctx, config.Source{
		Name: "scada", Type: "opcua", URL: fmt.Sprintf("opc.tcp://127.0.0.1:%d", opcPort), Poll: config.Duration{Duration: 200 * time.Millisecond},
		Points: []config.Point{{Node: "ns=1;s=dell.nvme.temp", Company: "huaduan", Plant: "p1", Asset: "RF-101", Field: "temp"}},
	}, hub, log)

	want := map[string]float64{"HP-101": 64, "HP-201": 67, "RF-101": 54.85}
	host, _ := os.Hostname()
	// MQTT values are credited to the publishing client (the edge), not to the broker
	src := map[string]string{"HP-101": "smartfactory-edge-" + host, "HP-201": "plc", "RF-101": "scada"}
	deadline := time.Now().Add(15 * time.Second)
	for {
		got := map[string]tags.Tag{}
		for _, tg := range hub.Last("huaduan", "p1", "") {
			got[tg.Asset] = tg
		}
		done := true
		for a, v := range want {
			tg, ok := got[a]
			if !ok || toF(tg.Value) != v || tg.Source != src[a] {
				done = false
			}
		}
		if done {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("tags did not arrive: %+v\nsources: %+v", got, hub.Sources())
		}
		time.Sleep(100 * time.Millisecond)
	}
	for _, s := range hub.Sources() {
		if !s.Connected {
			t.Errorf("source %s not connected: %s", s.Name, s.Error)
		}
	}

	// A stop command for HP-201 becomes a coil write on the edge's Modbus slave; read it back.
	hub.Command(tags.Command{Company: "huaduan", Plant: "p1", Asset: "HP-201", Cmd: "stop", By: "test"})
	got := make(chan any, 1)
	go adapters.RunModbus(ctx, config.Source{
		Name: "readback", Type: "modbus", URL: fmt.Sprintf("tcp://127.0.0.1:%d", modbusPort), Poll: config.Duration{Duration: 200 * time.Millisecond},
		Points: []config.Point{{Register: "coil", Address: 3, Company: "x", Plant: "x", Asset: "coil3", Field: "v"}},
	}, hub, log)
	go func() {
		for time.Now().Before(deadline) {
			if ts := hub.Last("x", "x", "coil3"); len(ts) == 1 && ts[0].Value == true {
				got <- true
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		got <- false
	}()
	if v := <-got; v != true {
		t.Fatal("stop command did not reach the Modbus coil")
	}
}

func toF(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	}
	return -1
}

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }
