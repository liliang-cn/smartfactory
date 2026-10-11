package edge

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Buffer keeps readings on disk while the broker cannot be reached, and gives them back in the
// order they were taken, with the time they were taken, once it can. It holds at most Max; past
// that the oldest go first and are counted in Dropped.
type Buffer struct {
	path string
	max  int

	mu      sync.Mutex
	n       int
	dropped uint64
}

type buffered struct {
	Topic string  `json:"topic"`
	V     float64 `json:"v"`
	T     int64   `json:"t"` // unix ms
}

// OpenBuffer opens (or starts) the buffer file at path.
func OpenBuffer(path string, max int) (*Buffer, error) {
	if max <= 0 {
		max = 100_000
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	b := &Buffer{path: path, max: max}
	if data, err := os.ReadFile(path); err == nil {
		b.n = bytes.Count(data, []byte{'\n'})
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return b, nil
}

// Len is how many readings are waiting; Dropped how many were given up to stay within Max.
func (b *Buffer) Len() int { b.mu.Lock(); defer b.mu.Unlock(); return b.n }

func (b *Buffer) Dropped() uint64 { b.mu.Lock(); defer b.mu.Unlock(); return b.dropped }

// Add keeps a reading taken at t.
func (b *Buffer) Add(topic string, v float64, t time.Time) error {
	line, _ := json.Marshal(buffered{Topic: topic, V: v, T: t.UnixMilli()})
	b.mu.Lock()
	defer b.mu.Unlock()
	f, err := os.OpenFile(b.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(append(line, '\n'))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	b.n++
	if b.n > b.max {
		// drop the oldest tenth at once rather than rewrite the file for every reading
		return b.keepLast(b.max * 9 / 10)
	}
	return nil
}

func (b *Buffer) keepLast(k int) error {
	lines, err := b.lines()
	if err != nil {
		return err
	}
	if len(lines) <= k {
		return nil
	}
	b.dropped += uint64(len(lines) - k)
	return b.rewrite(lines[len(lines)-k:])
}

func (b *Buffer) lines() ([][]byte, error) {
	data, err := os.ReadFile(b.path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out [][]byte
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		if len(sc.Bytes()) > 0 {
			out = append(out, append([]byte(nil), sc.Bytes()...))
		}
	}
	return out, sc.Err()
}

func (b *Buffer) rewrite(lines [][]byte) error {
	tmp := b.path + ".tmp"
	var buf bytes.Buffer
	for _, l := range lines {
		buf.Write(l)
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(tmp, buf.Bytes(), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, b.path); err != nil {
		return err
	}
	b.n = len(lines)
	return nil
}

// Drain sends the readings in order as {"v": value, "t": unix ms} payloads; the first that
// fails stops it, and it and the rest stay for the next time.
func (b *Buffer) Drain(send func(topic string, payload []byte) error) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	lines, err := b.lines()
	if err != nil || len(lines) == 0 {
		return 0, err
	}
	sent := 0
	for _, l := range lines {
		var r buffered
		if json.Unmarshal(l, &r) != nil {
			sent++ // a torn line from a crash: skip it
			continue
		}
		payload, _ := json.Marshal(map[string]any{"v": r.V, "t": r.T})
		if err := send(r.Topic, payload); err != nil {
			if rerr := b.rewrite(lines[sent:]); rerr != nil {
				return sent, rerr
			}
			return sent, err
		}
		sent++
	}
	return sent, b.rewrite(nil)
}
