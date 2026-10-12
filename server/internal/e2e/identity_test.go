package e2e

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"

	"smartfactory/internal/adapters"
	"smartfactory/internal/config"
	"smartfactory/internal/edge"
	"smartfactory/internal/mqttauth"
	"smartfactory/internal/tags"
)

// Devices with identities of their own on the embedded broker: a device with a token publishes
// readings of its own assets and hears their commands, and is refused another asset's topics
// (counted); one with a certificate — here the edge gateway — connects over TLS with no
// password; revoking either cuts it off at once and for good. A shared user still publishes
// anything, as before. The credentials only ever go to files.
func TestDeviceIdentity(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if os.Getenv("E2E_LOG") != "" {
		log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dir := t.TempDir()
	reg, err := mqttauth.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()

	port, tlsPort := freePort(t), freePort(t)
	hub := tags.NewHub()
	// no shared user and no device yet: this machine only, until a device is registered
	broker, err := adapters.StartBroker(config.Broker{Listen: fmt.Sprintf(":%d", port), Prefix: "sf",
		TLS: &config.BrokerTLS{Listen: fmt.Sprintf("127.0.0.1:%d", tlsPort)}}, hub, reg, log)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	if got := broker.Listening(); got != fmt.Sprintf("127.0.0.1:%d", port) {
		t.Fatalf("with no way to tell clients apart the broker listens on %s", got)
	}
	if st, err := os.Stat(filepath.Join(dir, "ca", "ca.key")); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("the CA key: %v %v", st, err)
	}

	secrets := t.TempDir()
	keep := func(name, body string) string {
		p := filepath.Join(secrets, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	read := func(p string) string { b, _ := os.ReadFile(p); return string(b) }

	rf, cred, err := reg.Add("厂长", "rf-line1", mqttauth.Token, mqttauth.Scope{Company: "huaduan", Plant: "p1", Assets: []string{"RF-*"}})
	if err != nil {
		t.Fatal(err)
	}
	tokenFile := keep("rf-line1.token", cred.Token)
	waitFor(t, "the broker to listen on the network once a device is registered", func() bool { return broker.Listening() == fmt.Sprintf(":%d", port) })

	url := fmt.Sprintf("tcp://127.0.0.1:%d", port)
	connect := func(id, user, pass string) (paho.Client, error) {
		c := paho.NewClient(paho.NewClientOptions().AddBroker(url).SetClientID(id).SetUsername(user).SetPassword(pass).
			SetAutoReconnect(false).SetConnectRetry(false).SetConnectTimeout(3 * time.Second))
		tok := c.Connect()
		tok.WaitTimeout(5 * time.Second)
		return c, tok.Error()
	}
	if _, err := connect("rf-line1", "rf-line1", "sfd_wrong"); err == nil {
		t.Fatal("a wrong token connected")
	}
	if _, err := connect("someone-else", "rf-line1", read(tokenFile)); err == nil {
		t.Fatal("a device connected under another client id")
	}
	if _, err := connect("anon", "", ""); err != nil {
		t.Fatalf("a client on this machine is refused while the broker has no shared users: %v", err)
	}
	if _, err := connect("rf-line1", "", ""); err == nil {
		t.Fatal("a client on this machine took a device's name without its token")
	}

	var lost atomic.Bool
	cmds := make(chan string, 4)
	dev := paho.NewClient(paho.NewClientOptions().AddBroker(url).SetClientID("rf-line1").SetUsername("rf-line1").SetPassword(read(tokenFile)).
		SetAutoReconnect(false).SetConnectionLostHandler(func(paho.Client, error) { lost.Store(true) }))
	if tok := dev.Connect(); !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
		t.Fatalf("the device with its token did not connect: %v", tok.Error())
	}
	defer dev.Disconnect(100)

	// its own asset: taken; another's: refused and counted
	before := listed(t, reg, "rf-line1").Refused
	dev.Publish("sf/huaduan/p1/RF-101/temp", 1, false, "61.5").WaitTimeout(5 * time.Second)
	dev.Publish("sf/huaduan/p1/HP-101/temp", 0, false, "99")
	waitFor(t, "the device's own reading", func() bool {
		ts := hub.Last("huaduan", "p1", "RF-101")
		return len(ts) == 1 && ts[0].Value == 61.5 && ts[0].Source == "rf-line1"
	})
	waitFor(t, "the refusal to be counted", func() bool { return listed(t, reg, "rf-line1").Refused == before+1 })
	if ts := hub.Last("huaduan", "p1", "HP-101"); len(ts) != 0 {
		t.Fatalf("another asset's reading got through: %+v", ts)
	}
	if d := listed(t, reg, "rf-line1"); !d.Connected || d.LastSeen == nil || d.LastRefusal != "发 sf/huaduan/p1/HP-101/temp" {
		t.Fatalf("listed: connected %v, seen %v, last refused %q", d.Connected, d.LastSeen, d.LastRefusal)
	}

	// commands: its own asset's only
	sub := dev.Subscribe("sf/huaduan/p1/+/cmd", 1, nil)
	sub.WaitTimeout(5 * time.Second)
	if r := sub.(*paho.SubscribeToken).Result()["sf/huaduan/p1/+/cmd"]; r != 0x80 {
		t.Fatalf("subscribing to every asset's commands answered %#x", r)
	}
	sub = dev.Subscribe("sf/huaduan/p1/RF-101/cmd", 1, func(_ paho.Client, m paho.Message) { cmds <- m.Topic() })
	sub.WaitTimeout(5 * time.Second)
	if r := sub.(*paho.SubscribeToken).Result()["sf/huaduan/p1/RF-101/cmd"]; r != 1 {
		t.Fatalf("subscribing to its own commands answered %#x", r)
	}
	hub.Command(tags.Command{Company: "huaduan", Plant: "p1", Asset: "RF-101", Cmd: "stop", By: "operator"})
	select {
	case topic := <-cmds:
		if topic != "sf/huaduan/p1/RF-101/cmd" {
			t.Fatalf("command on %s", topic)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the device did not hear its own command")
	}

	// a certificate device: the edge gateway over TLS, no password
	hp, cc, err := reg.Add("厂长", "edge-hp", mqttauth.Cert, mqttauth.Scope{Company: "huaduan", Plant: "p1", Assets: []string{"HP-101"}})
	if err != nil {
		t.Fatal(err)
	}
	certFile, keyFile, caFile := keep("edge-hp.crt", cc.Cert), keep("edge-hp.key", cc.Key), keep("ca.crt", cc.CA)
	var n atomic.Int64
	exporter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "node_hwmon_temp_celsius{chip=\"cpu\"} %d\n", 40+n.Add(1))
	}))
	defer exporter.Close()
	ecfgPath := keep("edge.json", fmt.Sprintf(`{
		"scrape": [%q], "every": "100ms",
		"metrics": {"cpu": {"name": "node_hwmon_temp_celsius"}},
		"mqtt": {"url": "tls://127.0.0.1:%d", "cert": %q, "key": %q, "ca": %q, "buffer": %q,
			"publish": [{"metric": "cpu", "topic": "sf/huaduan/p1/HP-101/temp"}]}
	}`, exporter.URL, tlsPort, certFile, keyFile, caFile, filepath.Join(secrets, "buffer.jsonl")))
	ecfg, err := edge.Load(ecfgPath)
	if err != nil {
		t.Fatal(err)
	}
	edgeCtx, stopEdge := context.WithCancel(ctx)
	defer stopEdge()
	go edge.Run(edgeCtx, ecfg, log)
	waitFor(t, "the gateway's reading over TLS", func() bool {
		ts := hub.Last("huaduan", "p1", "HP-101")
		return len(ts) == 1 && ts[0].Source == "edge-hp"
	})
	if d := listed(t, reg, "edge-hp"); !d.Connected || d.Expires == nil {
		t.Fatalf("listed: connected %v, expires %v", d.Connected, d.Expires)
	}

	// a client certificate the CA did not issue is no identity
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM([]byte(read(caFile)))
	tlsConnect := func(id, user, pass string, certs []tls.Certificate) error {
		c := paho.NewClient(paho.NewClientOptions().AddBroker(fmt.Sprintf("tls://127.0.0.1:%d", tlsPort)).SetClientID(id).SetUsername(user).SetPassword(pass).
			SetTLSConfig(&tls.Config{RootCAs: pool, Certificates: certs}).SetAutoReconnect(false).SetConnectTimeout(3 * time.Second))
		tok := c.Connect()
		tok.WaitTimeout(5 * time.Second)
		if tok.Error() == nil {
			c.Disconnect(50)
		}
		return tok.Error()
	}
	other, err := mqttauth.LoadCA(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	forged, err := other.IssueDevice("edge-hp")
	if err != nil {
		t.Fatal(err)
	}
	if err := tlsConnect("edge-hp", "", "", tlsPair(t, keep("forged.crt", string(forged.Cert)), keep("forged.key", string(forged.Key)))); err == nil {
		t.Fatal("a certificate from another CA connected")
	}
	// a token works over TLS too: the server's certificate is checked against the CA
	if err := tlsConnect("rf-line1", "rf-line1", read(tokenFile), nil); err != nil {
		t.Fatalf("a token over TLS: %v", err)
	}
	// that took over rf-line1's session (same client id): connect it again for the revocation
	waitFor(t, "the first session to be taken over", lost.Load)
	lost.Store(false)
	dev = paho.NewClient(paho.NewClientOptions().AddBroker(url).SetClientID("rf-line1").SetUsername("rf-line1").SetPassword(read(tokenFile)).
		SetAutoReconnect(false).SetConnectionLostHandler(func(paho.Client, error) { lost.Store(true) }))
	if tok := dev.Connect(); !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
		t.Fatalf("the device did not connect again: %v", tok.Error())
	}

	// revoked: both are cut off at once, and stay off
	if _, err := reg.Revoke("huaduan", rf.ID, "厂长"); err != nil {
		t.Fatal(err)
	}
	if _, err := reg.Revoke("huaduan", hp.ID, "厂长"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the token device to be disconnected", lost.Load)
	waitFor(t, "the gateway to be disconnected", func() bool {
		for _, s := range hub.Sources() {
			if s.Name == "edge-hp" {
				return !s.Connected
			}
		}
		return false
	})
	if _, err := connect("rf-line1", "rf-line1", read(tokenFile)); err == nil {
		t.Fatal("a revoked device's token connected")
	}
	if err := tlsConnect("edge-hp", "", "", tlsPair(t, certFile, keyFile)); err == nil {
		t.Fatal("a revoked device's certificate connected")
	}
	// the gateway keeps trying (paho reconnects) and is refused each time: no more readings
	last := hub.Last("huaduan", "p1", "HP-101")[0].At
	time.Sleep(1500 * time.Millisecond)
	if at := hub.Last("huaduan", "p1", "HP-101")[0].At; !at.Equal(last) {
		t.Fatal("a revoked gateway's readings still arrive")
	}
}

// A shared user is not held to any topics, as before devices had identities; a client without
// a password is refused once the broker has shared users.
func TestSharedUsers(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	reg, err := mqttauth.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	port := freePort(t)
	hub := tags.NewHub()
	broker, err := adapters.StartBroker(config.Broker{Listen: fmt.Sprintf("127.0.0.1:%d", port), Prefix: "sf", Users: map[string]string{"device": "pw"}}, hub, reg, log)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()
	if _, _, err := reg.Add("厂长", "device", mqttauth.Token, mqttauth.Scope{Company: "huaduan", Plant: "p1", Assets: []string{"*"}}); err == nil {
		t.Fatal("a device took a shared user's name")
	}
	opts := func(user, pass string) *paho.ClientOptions {
		return paho.NewClientOptions().AddBroker(fmt.Sprintf("tcp://127.0.0.1:%d", port)).SetClientID("plc-" + user).SetUsername(user).SetPassword(pass).
			SetAutoReconnect(false).SetConnectTimeout(3 * time.Second)
	}
	anon := paho.NewClient(opts("", ""))
	if tok := anon.Connect(); tok.WaitTimeout(5*time.Second) && tok.Error() == nil {
		t.Fatal("a client without a password connected")
	}
	c := paho.NewClient(opts("device", "pw"))
	if tok := c.Connect(); !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
		t.Fatalf("a shared user: %v", tok.Error())
	}
	defer c.Disconnect(100)
	c.Publish("sf/jianghe/p9/ANY-1/temp", 1, false, "12").WaitTimeout(5 * time.Second)
	waitFor(t, "a shared user's reading", func() bool { return len(hub.Last("jianghe", "p9", "ANY-1")) == 1 })
}

func listed(t *testing.T, reg *mqttauth.Registry, name string) mqttauth.Device {
	t.Helper()
	list, err := reg.List("huaduan")
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range list {
		if d.Name == name && d.Revoked == nil {
			return d
		}
	}
	return mqttauth.Device{}
}

func tlsPair(t *testing.T, cert, key string) []tls.Certificate {
	pair, err := tls.LoadX509KeyPair(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	return []tls.Certificate{pair}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); !ok(); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}
