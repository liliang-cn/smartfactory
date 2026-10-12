package web

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"

	"smartfactory/internal/adapters"
	"smartfactory/internal/audit"
	"smartfactory/internal/auth"
	"smartfactory/internal/config"
	"smartfactory/internal/engine"
	"smartfactory/internal/mqttauth"
	"smartfactory/internal/ops"
	"smartfactory/internal/sfptest"
	"smartfactory/internal/tags"
)

// A company's administrator registers a device for some of a plant's assets and gets its token
// (or its certificate bundle) once; the device connects with it and is listed as connected; a
// new token stops the old one; revoking it is in the activity log, which never holds the token.
// Nobody but the company's administrators may do any of it.
func TestDevices(t *testing.T) {
	if _, err := os.Stat("../engine/js/engine.js"); err != nil {
		t.Skip("engine bundle not built (npm run build:engine)")
	}
	std := sfptest.Load(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	eng, err := engine.New(log)
	if err != nil {
		t.Fatal(err)
	}
	eng.Dir = t.TempDir()
	eng.Single()
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); <-eng.Done })
	go eng.Run(ctx)
	trail, _ := audit.Open(t.TempDir(), log)
	t.Cleanup(func() { trail.Close() })
	reg, err := mqttauth.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reg.Close() })
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	l.Close()
	hub := tags.NewHub()
	broker, err := adapters.StartBroker(config.Broker{Listen: addr, Prefix: "sf", Users: map[string]string{"plc": "pw"}}, hub, reg, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { broker.Close() })

	admin, adk := auth.NewToken("厂长", auth.Admin, []string{"jianghe"}, "")
	other, ok := auth.NewToken("别家管理员", auth.Admin, []string{"huaduan"}, "")
	operator, opk := auth.NewToken("班长", auth.Operator, []string{"jianghe"}, "")
	root, rk := auth.NewToken("总管", auth.Admin, nil, "")
	ts := httptest.NewServer((&Server{Hub: hub, Engine: eng, Keys: keyring(t, "", adk, ok, opk, rk), Audit: trail, Log: log, Devices: reg,
		MQTT: &ops.MQTT{Prefix: "sf", Listen: broker.Listening, Shared: []string{"plc"}}}).Handler())
	t.Cleanup(ts.Close)
	do := func(token, method, path string, body any) (int, []byte) {
		t.Helper()
		var rd io.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		req, _ := http.NewRequest(method, ts.URL+path, rd)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return res.StatusCode, b
	}
	var cfg map[string]any
	json.Unmarshal([]byte(eng.BuiltinTemplate("huaduan")), &cfg)
	cfg["id"], cfg["name"], cfg["short"] = "jianghe", "江河锻造", "江河"
	if got, b := do(root, "POST", "/api/admin/companies", cfg); got != 200 {
		t.Fatalf("build: %d %s", got, b)
	}
	plant := cfg["plants"].([]any)[0].(map[string]any)["id"].(string)

	in := map[string]any{"name": "rf-line1", "plant": plant, "assets": []string{"RF-*", " HP-101 ", "RF-*"}, "kind": "token"}
	std.Check("RegisterDevice", mustJSON(in))
	for _, c := range []struct {
		token string
		code  int
	}{{"", 401}, {operator, 403}, {other, 403}} {
		if got, b := do(c.token, "POST", "/api/admin/devices/jianghe", in); got != c.code {
			t.Fatalf("registered by %q: %d %s", c.token, got, b)
		}
		if got, _ := do(c.token, "GET", "/api/admin/devices/jianghe", nil); got != c.code {
			t.Fatalf("listed by %q: %d", c.token, got)
		}
	}
	if got, b := do(admin, "POST", "/api/admin/devices/jianghe", map[string]any{"name": "x", "plant": "nowhere", "assets": []string{"*"}}); got != 422 {
		t.Fatalf("a plant that is not there: %d %s", got, b)
	}
	if got, b := do(admin, "POST", "/api/admin/devices/jianghe", map[string]any{"name": "plc", "plant": plant, "assets": []string{"*"}}); got != 422 {
		t.Fatalf("a shared user's name: %d %s", got, b)
	}

	got, b := do(admin, "POST", "/api/admin/devices/jianghe", in)
	if got != 200 {
		t.Fatalf("register: %d %s", got, b)
	}
	std.Check("DeviceCredential", b)
	var res ops.DeviceIssued
	json.Unmarshal(b, &res)
	if res.Token == "" || res.Cert != "" || res.Device.Kind != "token" || strings.Join(res.Device.Assets, ",") != "RF-*,HP-101" {
		t.Fatalf("registered %s %v with a token %v", res.Device.Kind, res.Device.Assets, res.Token != "")
	}
	token := res.Token
	id := res.Device.ID

	// it connects with its token, under its own name
	dev := paho.NewClient(paho.NewClientOptions().AddBroker("tcp://" + addr).SetClientID("rf-line1").SetUsername("rf-line1").SetPassword(token).SetAutoReconnect(false))
	if tok := dev.Connect(); !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
		t.Fatalf("the device did not connect: %v", tok.Error())
	}
	t.Cleanup(func() { dev.Disconnect(50) })
	dev.Publish("sf/jianghe/"+plant+"/HP-102/temp", 0, false, "1")
	var ov ops.DevicesOverview
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		_, b = do(admin, "GET", "/api/admin/devices/jianghe", nil)
		json.Unmarshal(b, &ov)
		if len(ov.Devices) == 1 && ov.Devices[0].Connected && ov.Devices[0].Refused == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("listed %s", b)
		}
	}
	std.Check("BrokerDevices", b)
	if len(ov.Shared) != 1 || ov.Shared[0] != "plc" || ov.Broker.Listen != addr || !ov.Broker.Local {
		t.Fatalf("broker %+v, shared %v", ov.Broker, ov.Shared)
	}

	// a certificate device: the bundle is its certificate, its key and the CA
	got, b = do(admin, "POST", "/api/admin/devices/jianghe", map[string]any{"name": "hp-plc", "plant": plant, "assets": []string{"HP-101"}, "kind": "cert"})
	if got != 200 {
		t.Fatalf("register a certificate device: %d %s", got, b)
	}
	std.Check("DeviceCredential", b)
	var cres ops.DeviceIssued
	json.Unmarshal(b, &cres)
	zr, err := zip.NewReader(bytes.NewReader(cres.Bundle), int64(len(cres.Bundle)))
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, f := range zr.File {
		files = append(files, f.Name)
	}
	sort.Strings(files)
	if strings.Join(files, " ") != "ca.crt hp-plc.crt hp-plc.key" || cres.Token != "" || !strings.Contains(cres.Key, "PRIVATE KEY") {
		t.Fatalf("the bundle holds %v", files)
	}
	if cres.Device.Expires == nil {
		t.Fatal("no expiry for the certificate")
	}

	// a new token: the old one stops, the connection on it is closed
	got, b = do(admin, "POST", "/api/admin/devices/jianghe/"+id+"/renew", nil)
	if got != 200 {
		t.Fatalf("renew: %d %s", got, b)
	}
	json.Unmarshal(b, &res)
	if res.Token == "" || res.Token == token {
		t.Fatal("renewing gave no new token")
	}
	for deadline := time.Now().Add(5 * time.Second); dev.IsConnectionOpen(); time.Sleep(50 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the device stayed connected on its old token")
		}
	}
	old := paho.NewClient(paho.NewClientOptions().AddBroker("tcp://" + addr).SetClientID("rf-line1").SetUsername("rf-line1").SetPassword(token).SetAutoReconnect(false))
	if tok := old.Connect(); tok.WaitTimeout(5*time.Second) && tok.Error() == nil {
		t.Fatal("the old token still connects")
	}

	if got, _ := do(other, "DELETE", "/api/admin/devices/jianghe/"+id, nil); got != 403 {
		t.Fatalf("another company's administrator revoked it: %d", got)
	}
	if got, _ := do(admin, "DELETE", "/api/admin/devices/huaduan/"+id, nil); got != 403 && got != 404 {
		t.Fatalf("revoked under another company: %d", got)
	}
	got, b = do(admin, "DELETE", "/api/admin/devices/jianghe/"+id, nil)
	if got != 200 {
		t.Fatalf("revoke: %d %s", got, b)
	}
	std.Check("BrokerDevice", b)
	if got, _ := do(admin, "DELETE", "/api/admin/devices/jianghe/"+id, nil); got != 404 {
		t.Fatalf("revoked twice: %d", got)
	}
	_, b = do(admin, "GET", "/api/admin/devices/jianghe", nil)
	json.Unmarshal(b, &ov)
	if len(ov.Devices) != 2 || ov.Devices[0].Name != "hp-plc" || ov.Devices[1].Revoked == nil || ov.Devices[1].RevokedBy != "厂长" {
		t.Fatalf("listed %s", b)
	}

	entries, _ := trail.List(t.Context(), "jianghe", 0, 100)
	var actions []string
	for _, e := range entries {
		if strings.HasPrefix(e.Action, "device.") && e.OK {
			actions = append(actions, e.Action+" "+e.Target+" "+e.Who)
		}
		raw, _ := json.Marshal(e)
		if strings.Contains(string(raw), token) || strings.Contains(string(raw), res.Token) || strings.Contains(string(raw), "PRIVATE KEY") {
			t.Fatal("a credential is in the activity log")
		}
	}
	want := []string{"device.revoke rf-line1 厂长", "device.renew rf-line1 厂长", "device.add hp-plc 厂长", "device.add rf-line1 厂长"}
	if strings.Join(actions, "|") != strings.Join(want, "|") {
		t.Fatalf("activity log: %v", actions)
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
