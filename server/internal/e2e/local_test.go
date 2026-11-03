package e2e

import (
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"

	"smartfactory/internal/adapters"
	"smartfactory/internal/config"
	"smartfactory/internal/mqttauth"
	"smartfactory/internal/tags"
)

// A client on the server's own machine without credentials: let in on an older installation
// only until a device is registered, never on one set up with accounts, and always when the
// config says so (development, emulated equipment). Refused, it publishes nothing.
func TestLocalClients(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	start := func(open bool) (*adapters.Broker, *mqttauth.Registry, *tags.Hub, int) {
		reg, err := mqttauth.Open(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { reg.Close() })
		port := freePort(t)
		hub := tags.NewHub()
		b, err := adapters.StartBroker(config.Broker{Listen: fmt.Sprintf("127.0.0.1:%d", port), Prefix: "sf", OpenLocal: open}, hub, reg, log)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { b.Close() })
		return b, reg, hub, port
	}
	connects := func(port int, id string) (paho.Client, bool) {
		c := paho.NewClient(paho.NewClientOptions().AddBroker(fmt.Sprintf("tcp://127.0.0.1:%d", port)).SetClientID(id).
			SetAutoReconnect(false).SetConnectTimeout(3 * time.Second))
		tok := c.Connect()
		return c, tok.WaitTimeout(5*time.Second) && tok.Error() == nil
	}
	register := func(reg *mqttauth.Registry) {
		if _, _, err := reg.Add("厂长", "rf-line1", mqttauth.Token, mqttauth.Scope{Company: "huaduan", Plant: "p1", Assets: []string{"RF-*"}}); err != nil {
			t.Fatal(err)
		}
	}

	// an older installation, nothing registered: as before
	_, reg, _, port := start(false)
	if c, ok := connects(port, "plc-1"); !ok {
		t.Fatal("a local client before any device is registered")
	} else {
		c.Disconnect(100)
	}
	register(reg)
	c, ok := connects(port, "nobody")
	if ok {
		c.Disconnect(100)
		t.Fatal("a local client without credentials after a device is registered")
	}

	// set up with accounts: never, not even before a device is registered
	b, _, hub, port := start(false)
	b.CloseLocal()
	if c, ok := connects(port, "nobody"); ok {
		c.Publish("sf/huaduan/p1/AS-101/temp", 1, false, "56").WaitTimeout(3 * time.Second)
		c.Disconnect(100)
		t.Fatal("a local client without credentials on an installation with accounts")
	}
	time.Sleep(200 * time.Millisecond)
	if len(hub.Last("huaduan", "p1", "AS-101")) != 0 {
		t.Fatal("a refused client's reading was taken")
	}

	// the config switch keeps the old trust
	_, reg, _, port = start(true)
	register(reg)
	if c, ok := connects(port, "dev-sim"); !ok {
		t.Fatal("openLocal: a local client was refused")
	} else {
		c.Disconnect(100)
	}
}
