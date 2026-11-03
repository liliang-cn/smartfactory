// Package health is how the server itself is doing: the process's CPU and memory, the data
// directory's size and the disk under it, sampled every ten seconds for the last hour, and how
// the browsers watching the twin are doing as they report it. The console's 运行状况 page shows it.
package health

import (
	"math"
	"os"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"slices"
	"strings"
	"sync"
	"time"
)

// Every is how often a sample is taken; Keep how many are kept (an hour).
const (
	Every = 10 * time.Second
	Keep  = 360
)

// Sample is the server at one moment.
type Sample struct {
	At time.Time `json:"at"`
	// CPU is the process's use of one core over the sample's interval, in percent (200 is two
	// cores busy); -1 where the system does not say.
	CPU float64 `json:"cpu"`
	// RSS is the memory the process holds; Heap what Go's own objects take of it.
	RSS  uint64 `json:"rss"`
	Heap uint64 `json:"heap"`
	// StepMs is how long the simulation's latest step took (it has 100 ms).
	StepMs     float64 `json:"stepMs"`
	Goroutines int     `json:"goroutines"`
	Viewers    int     `json:"viewers"`
	Devices    int     `json:"devices"`
	// ModelCalls is the requests sent to the language model since the previous sample.
	ModelCalls int64 `json:"modelCalls"`
}

// Probes are what the sampler reads besides the process itself; each may be nil.
type Probes struct {
	StepMs     func() float64
	Viewers    func() int
	Devices    func() int
	ModelCalls func() int64
}

// Sampler keeps the last hour of samples and the size of the data directory.
type Sampler struct {
	DataDir string
	Probes  Probes
	Start   time.Time

	mu       sync.Mutex
	ring     []Sample
	lastCPU  time.Duration
	lastAt   time.Time
	lastCall int64
	dir      DirUsage
	dirAt    time.Time

	Browsers *Browsers
}

func New(dir string, p Probes) *Sampler {
	s := &Sampler{DataDir: dir, Probes: p, Start: time.Now(), Browsers: NewBrowsers()}
	s.lastCPU, _ = cpuTime()
	s.lastAt = time.Now()
	if p.ModelCalls != nil {
		s.lastCall = p.ModelCalls()
	}
	return s
}

// Run samples every Every until done is closed.
func (s *Sampler) Run(done <-chan struct{}) {
	t := time.NewTicker(Every)
	defer t.Stop()
	s.Take(time.Now())
	for {
		select {
		case <-done:
			return
		case now := <-t.C:
			s.Take(now)
		}
	}
}

// Take adds a sample taken at now.
func (s *Sampler) Take(now time.Time) Sample {
	smp := Sample{At: now, CPU: -1, Goroutines: runtime.NumGoroutine()}
	cpu, ok := cpuTime()
	s.mu.Lock()
	defer s.mu.Unlock()
	if ok {
		if wall := now.Sub(s.lastAt); wall > 0 && !s.lastAt.IsZero() {
			smp.CPU = math.Round(float64(cpu-s.lastCPU)/float64(wall)*1000) / 10
		}
		s.lastCPU, s.lastAt = cpu, now
	}
	smp.RSS, smp.Heap = memory()
	p := s.Probes
	if p.StepMs != nil {
		smp.StepMs = math.Round(p.StepMs()*10) / 10
	}
	if p.Viewers != nil {
		smp.Viewers = p.Viewers()
	}
	if p.Devices != nil {
		smp.Devices = p.Devices()
	}
	if p.ModelCalls != nil {
		n := p.ModelCalls()
		smp.ModelCalls, s.lastCall = n-s.lastCall, n
	}
	s.ring = append(s.ring, smp)
	if len(s.ring) > Keep {
		s.ring = slices.Delete(s.ring, 0, len(s.ring)-Keep)
	}
	return smp
}

// Samples is the last hour, oldest first.
func (s *Sampler) Samples() []Sample {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.ring)
}

// Summary is the samples of the last d boiled down.
type Summary struct {
	CPU       float64 `json:"cpu"`
	CPUMax    float64 `json:"cpuMax"`
	StepMs    float64 `json:"stepMs"`
	StepMaxMs float64 `json:"stepMaxMs"`
	Calls     int64   `json:"calls"`
}

// Summarize averages the samples taken in the d before now (CPU and step time) and adds up the
// model calls; samples where the system gave no CPU figure are left out of its average.
func Summarize(ss []Sample, now time.Time, d time.Duration) Summary {
	var out Summary
	var cpuN, stepN int
	for _, s := range ss {
		if now.Sub(s.At) > d {
			continue
		}
		if s.CPU >= 0 {
			out.CPU += s.CPU
			out.CPUMax = max(out.CPUMax, s.CPU)
			cpuN++
		}
		out.StepMs += s.StepMs
		out.StepMaxMs = max(out.StepMaxMs, s.StepMs)
		stepN++
		out.Calls += s.ModelCalls
	}
	if cpuN > 0 {
		out.CPU = math.Round(out.CPU/float64(cpuN)*10) / 10
	} else {
		out.CPU = -1
	}
	if stepN > 0 {
		out.StepMs = math.Round(out.StepMs/float64(stepN)*10) / 10
	}
	return out
}

// DirUsage is what the data directory takes, by what keeps it.
type DirUsage struct {
	Total int64   `json:"total"`
	Parts []Usage `json:"parts"`
	// Free and Size are the disk the directory is on; 0 where the system does not say.
	Free uint64 `json:"free"`
	Size uint64 `json:"size"`
}

type Usage struct {
	Name  string `json:"name"`
	Bytes int64  `json:"bytes"`
}

// Dir is the data directory's usage, measured at most every five minutes (the readings of a
// busy factory are many files).
func (s *Sampler) Dir() DirUsage {
	s.mu.Lock()
	if !s.dirAt.IsZero() && time.Since(s.dirAt) < 5*time.Minute {
		d := s.dir
		s.mu.Unlock()
		return d
	}
	s.mu.Unlock()
	d := MeasureDir(s.DataDir)
	s.mu.Lock()
	s.dir, s.dirAt = d, time.Now()
	s.mu.Unlock()
	return d
}

// MeasureDir sizes each entry of dir: a SQLite database with its -wal and -shm files counts as
// one, a directory as everything under it.
func MeasureDir(dir string) DirUsage {
	var out DirUsage
	if dir == "" {
		return out
	}
	out.Free, out.Size = disk(dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	by := map[string]int64{}
	for _, e := range entries {
		name := e.Name()
		var n int64
		if e.IsDir() {
			filepath.WalkDir(filepath.Join(dir, name), func(_ string, d os.DirEntry, err error) error {
				if err == nil && !d.IsDir() {
					if fi, err := d.Info(); err == nil {
						n += fi.Size()
					}
				}
				return nil
			})
		} else if fi, err := e.Info(); err == nil {
			n = fi.Size()
		}
		for _, suf := range []string{"-wal", "-shm", "-journal"} {
			name = strings.TrimSuffix(name, suf)
		}
		by[name] += n
		out.Total += n
	}
	for name, n := range by {
		out.Parts = append(out.Parts, Usage{name, n})
	}
	slices.SortFunc(out.Parts, func(a, b Usage) int {
		if a.Bytes != b.Bytes {
			return int(b.Bytes - a.Bytes)
		}
		return strings.Compare(a.Name, b.Name)
	})
	return out
}

// Host is the machine: its cores and memory (0 where the system does not say).
type Host struct {
	CPUs     int    `json:"cpus"`
	OS       string `json:"os"`
	MemTotal uint64 `json:"memTotal"`
	MemFree  uint64 `json:"memFree"`
}

func HostInfo() Host {
	total, free := hostMemory()
	return Host{CPUs: runtime.NumCPU(), OS: runtime.GOOS + "/" + runtime.GOARCH, MemTotal: total, MemFree: free}
}

// goMemory is what the Go runtime has mapped and what its heap objects take: the process's
// memory where the system does not report its resident size.
func goMemory() (total, heap uint64) {
	s := []metrics.Sample{{Name: "/memory/classes/total:bytes"}, {Name: "/memory/classes/heap/objects:bytes"}}
	metrics.Read(s)
	if s[0].Value.Kind() == metrics.KindUint64 {
		total = s[0].Value.Uint64()
	}
	if s[1].Value.Kind() == metrics.KindUint64 {
		heap = s[1].Value.Uint64()
	}
	return total, heap
}
