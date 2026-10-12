package e2e

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"smartfactory/internal/adapters"
	"smartfactory/internal/config"
	"smartfactory/internal/edge"
	"smartfactory/internal/tags"
)

// While the server's broker is down the edge gateway keeps its readings on disk; when the broker
// comes back they arrive in order, each with the time it was taken, and the file is emptied.
func TestEdgeStoreAndForward(t *testing.T) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	var n atomic.Int64
	exporter := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "node_hwmon_temp_celsius{chip=\"cpu\"} %d\n", 40+n.Add(1))
	}))
	defer exporter.Close()

	port := freePort(t)
	buffer := filepath.Join(t.TempDir(), "buffer.jsonl")
	var ecfg edge.Config
	if err := jsonUnmarshal(fmt.Sprintf(`{
		"scrape": [%q], "every": "100ms",
		"metrics": {"cpu": {"name": "node_hwmon_temp_celsius"}},
		"mqtt": {"url": "tcp://127.0.0.1:%d", "publish": [{"metric": "cpu", "topic": "sf/huaduan/p1/HP-101/temp"}], "buffer": %q}
	}`, exporter.URL, port, buffer), &ecfg); err != nil {
		t.Fatal(err)
	}
	go edge.Run(ctx, ecfg, log)

	// the broker is down for a while
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		b, _ := os.ReadFile(buffer)
		if countLines(b) >= 10 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the gateway kept %d readings while the broker was down", countLines(b))
		}
	}
	up := time.Now()

	hub := tags.NewHub()
	var mu sync.Mutex
	var got []tags.Tag
	hub.Tap = func(tg tags.Tag) {
		mu.Lock()
		got = append(got, tg)
		mu.Unlock()
	}
	broker, err := adapters.StartBroker(config.Broker{Listen: fmt.Sprintf("127.0.0.1:%d", port), Prefix: "sf"}, hub, nil, log)
	if err != nil {
		t.Fatal(err)
	}
	defer broker.Close()

	var early []tags.Tag
	for deadline := time.Now().Add(30 * time.Second); ; time.Sleep(200 * time.Millisecond) {
		mu.Lock()
		early = early[:0]
		for _, tg := range got {
			if tg.At.Before(up) {
				early = append(early, tg)
			}
		}
		mu.Unlock()
		b, _ := os.ReadFile(buffer)
		if len(early) >= 10 && countLines(b) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after the broker came back: %d readings from before it, %d still in the buffer", len(early), countLines(b))
		}
	}
	for i := 1; i < len(early); i++ {
		if !early[i].At.After(early[i-1].At) || toF(early[i].Value) <= toF(early[i-1].Value) {
			t.Fatalf("replayed out of order: %v then %v", early[i-1], early[i])
		}
	}
	if gap := early[1].At.Sub(early[0].At); gap < 50*time.Millisecond || gap > time.Second {
		t.Errorf("replayed readings %v apart, taken 100ms apart", gap)
	}
}

func countLines(b []byte) int {
	n := 0
	for _, c := range b {
		if c == '\n' {
			n++
		}
	}
	return n
}
