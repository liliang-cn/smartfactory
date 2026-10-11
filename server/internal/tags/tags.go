// Package tags is the hub every protocol adapter feeds and every consumer (browsers, MCP) reads.
//
// A tag is one value of one field of one asset, addressed the same way the twin addresses
// assets: company / plant / asset id / field. Adapters translate their protocol's addressing
// (MQTT topics, Modbus registers, OPC UA node ids) into this shape; nothing downstream knows
// which protocol a value came from except through Source.
package tags

import (
	"sort"
	"sync"
	"time"
)

// Tag is one live value.
type Tag struct {
	Company string `json:"c"`
	Plant   string `json:"p"`
	Asset   string `json:"a"`
	Field   string `json:"f"`
	Value   any    `json:"v"`
	Source  string `json:"src"`
	// T is At in unix milliseconds, for clients.
	T  int64     `json:"t"`
	At time.Time `json:"-"`
	// Q flags a reading that is kept but not believed (FlagRange, FlagSpike) or whose own time
	// could not be used (FlagClock).
	Q uint8 `json:"q,omitempty"`
}

// Quality flags.
const (
	// FlagRange: outside the plausible range of the field.
	FlagRange uint8 = 1 << iota
	// FlagSpike: changed faster than the field can.
	FlagSpike
	// FlagClock: the device's timestamp was unusable (in the future); it carries arrival time.
	FlagClock
)

// Doubtful is a flag that keeps a reading from the twin.
const Doubtful = FlagRange | FlagSpike

// Late is how old a reading may be and still count as live: older ones (a gateway replaying
// what it buffered) are kept but do not move the twin.
const Late = time.Minute

func (t Tag) key() string { return t.Company + "/" + t.Plant + "/" + t.Asset + "/" + t.Field }

// Command is an operator action on an asset, from a browser or an MCP client.
type Command struct {
	Company string   `json:"c"`
	Plant   string   `json:"p"`
	Asset   string   `json:"a"`
	Cmd     string   `json:"cmd"`
	Arg     *float64 `json:"arg,omitempty"`
	By      string   `json:"by"`
	// Name is the person or client behind an operator's or MCP command.
	Name string `json:"n,omitempty"`
	// Origin is the browser that issued it, so that browser can skip the echo.
	Origin string `json:"o,omitempty"`
}

// Event is what subscribers receive.
type Event struct {
	Type string   `json:"type"` // "tag" | "cmd"
	Tag  *Tag     `json:"tag,omitempty"`
	Cmd  *Command `json:"cmd,omitempty"`
}

// SourceStatus is reported by each adapter.
type SourceStatus struct {
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	URL       string    `json:"url"`
	Connected bool      `json:"connected"`
	Error     string    `json:"error,omitempty"`
	Points    int       `json:"points"`
	Received  uint64    `json:"received"`
	LastAt    time.Time `json:"lastAt"`
	// Flagged readings were kept but not given to the twin; LastFlag says why the last one was.
	Flagged  uint64 `json:"flagged,omitempty"`
	LastFlag string `json:"lastFlag,omitempty"`
	// Targets are the assets this source feeds, as company/plant/asset.
	Targets []string `json:"targets"`
	// Labels name each target for people ("华锻集团 一厂 · HP-101"); filled by the web layer.
	Labels []string `json:"labels,omitempty"`
}

// Sub is one subscriber's mailbox. Slow subscribers lose events rather than block the hub.
type Sub struct {
	C       chan Event
	dropped uint64
}

type Hub struct {
	// Check judges a reading before it is published (sets Q, returns why when it flags it).
	Check func(t *Tag) string
	// Tap sees every reading, flagged and late ones included (the series store keeps them). It
	// must not block.
	Tap func(Tag)

	mu      sync.RWMutex
	last    map[string]Tag
	subs    map[*Sub]struct{}
	cmdFns  []func(Command)
	sources map[string]*SourceStatus
}

func NewHub() *Hub {
	return &Hub{last: map[string]Tag{}, subs: map[*Sub]struct{}{}, sources: map[string]*SourceStatus{}}
}

// Publish records a value and fans it out. A doubtful or late reading only reaches Tap: the
// twin, the browsers and the current values carry on with the last good one.
func (h *Hub) Publish(t Tag) {
	now := time.Now()
	if t.At.IsZero() {
		t.At = now
	}
	why := ""
	if h.Check != nil {
		why = h.Check(&t)
	}
	t.T = t.At.UnixMilli()
	live := t.Q&Doubtful == 0 && now.Sub(t.At) < Late
	h.mu.Lock()
	if prev, ok := h.last[t.key()]; live && (!ok || !t.At.Before(prev.At)) {
		h.last[t.key()] = t
	} else {
		live = live && !ok
	}
	if s := h.sources[t.Source]; s != nil {
		s.Received++
		if t.At.After(s.LastAt) {
			s.LastAt = t.At
		}
		if t.Q&Doubtful != 0 {
			s.Flagged++
			s.LastFlag = why
		}
	}
	h.mu.Unlock()
	if h.Tap != nil {
		h.Tap(t)
	}
	if live {
		h.broadcast(Event{Type: "tag", Tag: &t})
	}
}

// Command hands an operator action to every adapter that can act on it and to every browser.
func (h *Hub) Command(c Command) {
	h.mu.RLock()
	fns := append([]func(Command){}, h.cmdFns...)
	h.mu.RUnlock()
	for _, fn := range fns {
		fn(c)
	}
	h.broadcast(Event{Type: "cmd", Cmd: &c})
}

// OnCommand registers an adapter's command handler.
func (h *Hub) OnCommand(fn func(Command)) {
	h.mu.Lock()
	h.cmdFns = append(h.cmdFns, fn)
	h.mu.Unlock()
}

func (h *Hub) broadcast(e Event) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for s := range h.subs {
		select {
		case s.C <- e:
		default:
			s.dropped++
		}
	}
}

func (h *Hub) Subscribe() *Sub {
	s := &Sub{C: make(chan Event, 512)}
	h.mu.Lock()
	h.subs[s] = struct{}{}
	h.mu.Unlock()
	return s
}

func (h *Hub) Unsubscribe(s *Sub) {
	h.mu.Lock()
	delete(h.subs, s)
	h.mu.Unlock()
}

// Last returns current values, optionally filtered; empty filter fields match everything.
func (h *Hub) Last(company, plant, asset string) []Tag {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]Tag, 0, len(h.last))
	for _, t := range h.last {
		if (company == "" || t.Company == company) && (plant == "" || t.Plant == plant) && (asset == "" || t.Asset == asset) {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].key() < out[j].key() })
	return out
}

// Source returns the status record for an adapter, creating it on first use.
func (h *Hub) Source(name, typ, url string, points int) *SourceStatus {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := h.sources[name]
	if s == nil {
		s = &SourceStatus{Name: name, Type: typ, URL: url}
		h.sources[name] = s
	}
	s.Points = points
	return s
}

// HasSource reports whether an adapter of that name has registered.
func (h *Hub) HasSource(name string) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.sources[name] != nil
}

// SetConnected updates an adapter's link state.
func (h *Hub) SetConnected(name string, ok bool, err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s := h.sources[name]; s != nil {
		s.Connected = ok
		s.Error = ""
		if err != nil {
			s.Error = err.Error()
		}
	}
}

func (h *Hub) Sources() []SourceStatus {
	h.mu.RLock()
	defer h.mu.RUnlock()
	targets := map[string]map[string]bool{}
	for _, t := range h.last {
		if targets[t.Source] == nil {
			targets[t.Source] = map[string]bool{}
		}
		targets[t.Source][t.Company+"/"+t.Plant+"/"+t.Asset] = true
	}
	out := make([]SourceStatus, 0, len(h.sources))
	for _, s := range h.sources {
		st := *s
		st.Targets = []string{}
		for k := range targets[s.Name] {
			st.Targets = append(st.Targets, k)
		}
		sort.Strings(st.Targets)
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
