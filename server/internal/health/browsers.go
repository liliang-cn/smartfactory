package health

import (
	"slices"
	"sync"
	"time"
)

// Report is what a browser showing the twin says about itself once a minute.
type Report struct {
	// ID is the page's own random id: one per open tab, nothing about who it is.
	ID string `json:"id"`
	// FPS is the frames drawn per second over the last minute (the median of its seconds).
	FPS float64 `json:"fps"`
	// HeapMB is the page's JavaScript memory, where the browser says (0 otherwise).
	HeapMB float64 `json:"heapMB"`
	Width  int     `json:"width"`
	Height int     `json:"height"`
	DPR    float64 `json:"dpr"`
	// Calls and Triangles are what one frame of the 3D view draws.
	Calls     int    `json:"calls"`
	Triangles int    `json:"triangles"`
	Mobile    bool   `json:"mobile"`
	Company   string `json:"company"`

	At time.Time `json:"at"`
}

// valid keeps out what no browser would send.
func (r Report) valid() bool {
	return len(r.ID) >= 8 && len(r.ID) <= 64 && r.FPS >= 0 && r.FPS <= 1000 && r.HeapMB >= 0 && r.HeapMB < 1<<20 &&
		r.Width >= 0 && r.Width < 1<<16 && r.Height >= 0 && r.Height < 1<<16 && r.DPR >= 0 && r.DPR <= 16 &&
		r.Calls >= 0 && r.Triangles >= 0 && len(r.Company) <= 64
}

// Browsers are the latest reports, each kept until its tab has been quiet for three minutes.
type Browsers struct {
	mu   sync.Mutex
	last map[string]Report
}

const (
	browserTTL  = 3 * time.Minute
	maxBrowsers = 500
	// SlowFPS is the frame rate under which a browser is struggling.
	SlowFPS = 24
)

func NewBrowsers() *Browsers { return &Browsers{last: map[string]Report{}} }

// Add keeps r (received at now); false when it is not a plausible report or there are too many.
func (b *Browsers) Add(r Report, now time.Time) bool {
	if !r.valid() {
		return false
	}
	r.At = now
	b.mu.Lock()
	defer b.mu.Unlock()
	b.prune(now)
	if _, ok := b.last[r.ID]; !ok && len(b.last) >= maxBrowsers {
		return false
	}
	b.last[r.ID] = r
	return true
}

func (b *Browsers) prune(now time.Time) {
	for id, r := range b.last {
		if now.Sub(r.At) > browserTTL {
			delete(b.last, id)
		}
	}
}

// BrowserSummary is how the watching browsers are doing.
type BrowserSummary struct {
	Count     int      `json:"count"`
	MedianFPS float64  `json:"medianFps"`
	MedianMB  float64  `json:"medianHeapMB"`
	Mobile    int      `json:"mobile"`
	Slow      []Report `json:"slow"`
}

// Summary is the browsers that reported in the last three minutes; the slow ones, slowest first.
func (b *Browsers) Summary(now time.Time) BrowserSummary {
	b.mu.Lock()
	b.prune(now)
	rs := make([]Report, 0, len(b.last))
	for _, r := range b.last {
		rs = append(rs, r)
	}
	b.mu.Unlock()
	out := BrowserSummary{Count: len(rs), Slow: []Report{}}
	if len(rs) == 0 {
		return out
	}
	fps := make([]float64, 0, len(rs))
	mb := make([]float64, 0, len(rs))
	for _, r := range rs {
		fps = append(fps, r.FPS)
		if r.HeapMB > 0 {
			mb = append(mb, r.HeapMB)
		}
		if r.Mobile {
			out.Mobile++
		}
		if r.FPS < SlowFPS {
			out.Slow = append(out.Slow, r)
		}
	}
	out.MedianFPS, out.MedianMB = median(fps), median(mb)
	slices.SortFunc(out.Slow, func(a, b Report) int {
		switch {
		case a.FPS < b.FPS:
			return -1
		case a.FPS > b.FPS:
			return 1
		}
		return 0
	})
	if len(out.Slow) > 10 {
		out.Slow = out.Slow[:10]
	}
	return out
}

func median(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	slices.Sort(xs)
	n := len(xs)
	if n%2 == 1 {
		return xs[n/2]
	}
	return (xs[n/2-1] + xs[n/2]) / 2
}
