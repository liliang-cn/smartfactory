package edge

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// The buffer keeps readings in order across a reopen, holds at most Max (the oldest go), and a
// send that fails part way keeps what was not sent.
func TestBuffer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "b.jsonl")
	b, err := OpenBuffer(path, 10)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 11, 8, 0, 0, 0, time.UTC)
	for i := range 25 {
		if err := b.Add("sf/c/p1/HP-101/temp", float64(i), start.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	if b.Len() > 10 || b.Dropped() < 15 {
		t.Fatalf("over max: %d kept, %d dropped", b.Len(), b.Dropped())
	}
	b, _ = OpenBuffer(path, 10) // a restart of the gateway
	kept := b.Len()
	var got []float64
	var at []int64
	calls := 0
	_, err = b.Drain(func(topic string, payload []byte) error {
		calls++
		if calls == 4 {
			return errors.New("broker went away")
		}
		var m struct {
			V float64
			T int64
		}
		json.Unmarshal(payload, &m)
		got, at = append(got, m.V), append(at, m.T)
		return nil
	})
	if err == nil || len(got) != 3 || b.Len() != kept-3 {
		t.Fatalf("a failed send: err %v, sent %v, %d left of %d", err, got, b.Len(), kept)
	}
	if got[0] != 25-float64(kept) || at[1]-at[0] != 1000 {
		t.Errorf("not the newest in order with their times: %v %v", got, at)
	}
	n, err := b.Drain(func(string, []byte) error { return nil })
	if err != nil || n != kept-3 || b.Len() != 0 {
		t.Errorf("second drain: %d sent, %v, %d left", n, err, b.Len())
	}
}
