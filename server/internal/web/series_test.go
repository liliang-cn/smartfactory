package web

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
	"strings"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"

	"smartfactory/internal/adapters"
	"smartfactory/internal/auth"
	"smartfactory/internal/config"
	"smartfactory/internal/engine"
	"smartfactory/internal/series"
	"smartfactory/internal/sfptest"
	"smartfactory/internal/tags"
)

// A device publishes readings, some with the time it took them, one no machine could give;
// all are kept and read back through the API, the implausible one flagged, counted against
// its source and kept from the twin.
func TestSeriesEndToEnd(t *testing.T) {
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
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(func() { cancel(); <-eng.Done })
	go eng.Run(ctx)

	hub := tags.NewHub()
	v := &series.Validator{Machines: eng.Machines}
	hub.Check = v.Check
	store, err := series.Open(t.TempDir(), series.RetentionFrom(config.Series{}), log)
	if err != nil {
		t.Fatal(err)
	}
	hub.Tap = store.Add
	store.Start(ctx)
	t.Cleanup(func() { store.Close() })

	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	broker, err := adapters.StartBroker(config.Broker{Listen: fmt.Sprintf("127.0.0.1:%d", port), Prefix: "sf"}, hub, nil, log)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { broker.Close() })

	member, mk := auth.NewToken("计划员", auth.Member, []string{"huaduan"}, "")
	ts := httptest.NewServer((&Server{Hub: hub, Engine: eng, Keys: keyring(t, "", mk), Log: log, Series: store}).Handler())
	t.Cleanup(ts.Close)
	get := func(token, path string) (int, []byte) {
		req, _ := http.NewRequest("GET", ts.URL+path, nil)
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

	dev := paho.NewClient(paho.NewClientOptions().AddBroker(fmt.Sprintf("tcp://127.0.0.1:%d", port)).SetClientID("press-101"))
	if tok := dev.Connect(); !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
		t.Fatalf("connect: %v", tok.Error())
	}
	defer dev.Disconnect(100)
	pub := func(topic, payload string) {
		if tok := dev.Publish(topic, 1, false, payload); !tok.WaitTimeout(5*time.Second) || tok.Error() != nil {
			t.Fatalf("publish: %v", tok.Error())
		}
	}
	took := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	for i := range 10 {
		pub("sf/huaduan/p1/HP-101/temp", fmt.Sprintf(`{"v": %d, "t": %d}`, 60+i, took.Add(time.Duration(i)*time.Second).UnixMilli()))
	}
	pub("sf/huaduan/p1/HP-101/temp", "75")
	pub("sf/huaduan/p1/HP-101/temp", "99999")
	pub("sf/huaduan/p1/HP-101", fmt.Sprintf(`{"state": "running", "power": 120.5, "t": %d}`, took.Add(time.Minute).UnixMilli()))

	path := fmt.Sprintf("/api/series/huaduan/p1/HP-101?field=temp,power,state&from=%d", took.Add(-time.Minute).UnixMilli())
	var a series.Answer
	var body []byte
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		code, b := get(member, path)
		if code != 200 {
			t.Fatalf("series: %d %s", code, b)
		}
		body = b
		json.Unmarshal(b, &a)
		if len(a.Series) == 3 && len(a.Series[0].Points) == 12 && len(a.Series[1].Points) == 1 && len(a.Events) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("readings did not come back: %s", b)
		}
	}
	std.Check("SeriesAnswer", body)
	if a.Resolution != series.Raw {
		t.Errorf("a 2-hour range came back as %s", a.Resolution)
	}
	temp := a.Series[0].Points
	for i := range 10 {
		if int64(temp[i][0].(float64)) != took.Add(time.Duration(i)*time.Second).UnixMilli() || temp[i][1].(float64) != float64(60+i) {
			t.Errorf("reading %d: %v, want %d at the device's time", i, temp[i], 60+i)
		}
	}
	var flagged []any
	for _, p := range temp {
		if p[2].(float64) != 0 {
			flagged = p
		}
	}
	if flagged == nil || flagged[1].(float64) != 99999 || flagged[2].(float64) != float64(tags.FlagRange) {
		t.Errorf("the implausible reading: %v", temp)
	}
	if a.Events[0].V != "running" || a.Events[0].Src != "press-101" || a.Events[0].T != took.Add(time.Minute).UnixMilli() {
		t.Errorf("state: %+v", a.Events[0])
	}

	// kept from the twin: the current value is the last good one, and the source says why
	for _, tg := range hub.Last("huaduan", "p1", "HP-101") {
		if tg.Field == "temp" && tg.Value != 75.0 {
			t.Errorf("current temperature %v, want 75", tg.Value)
		}
	}
	_, b := get(member, "/api/health")
	std.Check("Health", b)
	var h struct {
		Sources []tags.SourceStatus
	}
	json.Unmarshal(b, &h)
	if len(h.Sources) != 1 || h.Sources[0].Flagged != 1 || !strings.Contains(h.Sources[0].LastFlag, "99999") {
		t.Errorf("source status: %s", b)
	}

	if code, _ := get("", path); code != 401 {
		t.Errorf("readings without a token: %d", code)
	}
	if code, _ := get(member, "/api/series/huaduan/p1/HP-101?step=week"); code != 400 {
		t.Errorf("a bad step: %d", code)
	}
	code, b := get(member, "/api/series/huaduan/p1/HP-101?step=minute&from="+fmt.Sprint(took.Add(-time.Minute).UnixMilli()))
	std.Check("SeriesAnswer", b)
	json.Unmarshal(b, &a)
	if code != 200 || a.Resolution != series.Minute || len(a.Fields) != 3 {
		t.Errorf("minutes: %d %s", code, b)
	}
}
