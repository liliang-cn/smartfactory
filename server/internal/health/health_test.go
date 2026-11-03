package health

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSamplerKeepsAnHour(t *testing.T) {
	calls := int64(0)
	s := New("", Probes{StepMs: func() float64 { return 12.34 }, ModelCalls: func() int64 { return calls }})
	t0 := time.Now()
	for i := 1; i <= Keep+20; i++ {
		calls += 2
		s.Take(t0.Add(time.Duration(i) * Every))
	}
	ss := s.Samples()
	if len(ss) != Keep {
		t.Fatalf("kept %d samples, want %d", len(ss), Keep)
	}
	if !ss[0].At.Before(ss[len(ss)-1].At) {
		t.Error("samples are not oldest first")
	}
	if ss[len(ss)-1].StepMs != 12.3 || ss[len(ss)-1].ModelCalls != 2 {
		t.Errorf("last sample %+v", ss[len(ss)-1])
	}
	if ss[len(ss)-1].RSS == 0 {
		t.Error("no memory figure")
	}
}

func TestSummarize(t *testing.T) {
	now := time.Now()
	ss := []Sample{
		{At: now.Add(-5 * time.Minute), CPU: 90, StepMs: 50, ModelCalls: 7},
		{At: now.Add(-50 * time.Second), CPU: 10, StepMs: 4, ModelCalls: 1},
		{At: now.Add(-20 * time.Second), CPU: -1, StepMs: 6, ModelCalls: 2},
		{At: now.Add(-10 * time.Second), CPU: 30, StepMs: 8},
	}
	s := Summarize(ss, now, time.Minute)
	if s.CPU != 20 || s.CPUMax != 30 || s.StepMs != 6 || s.StepMaxMs != 8 || s.Calls != 3 {
		t.Errorf("summary %+v", s)
	}
	if s := Summarize(nil, now, time.Minute); s.CPU != -1 {
		t.Errorf("empty summary CPU %v, want -1 (unknown)", s.CPU)
	}
}

func TestMeasureDirGroupsDatabases(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, n int) {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755)
		os.WriteFile(filepath.Join(dir, name), make([]byte, n), 0o644)
	}
	write("checks.db", 100)
	write("checks.db-wal", 50)
	write("checks.db-shm", 10)
	write("series/a/1.chunk", 400)
	write("series/b/2.chunk", 600)
	d := MeasureDir(dir)
	if d.Total != 1160 {
		t.Errorf("total %d", d.Total)
	}
	if len(d.Parts) != 2 || d.Parts[0].Name != "series" || d.Parts[0].Bytes != 1000 || d.Parts[1].Name != "checks.db" || d.Parts[1].Bytes != 160 {
		t.Errorf("parts %+v", d.Parts)
	}
	if d.Size == 0 || d.Free == 0 || d.Free > d.Size {
		t.Errorf("disk free %d of %d", d.Free, d.Size)
	}
}

func TestBrowsers(t *testing.T) {
	b := NewBrowsers()
	now := time.Now()
	for i, fps := range []float64{60, 58, 12, 30, 75} {
		if !b.Add(Report{ID: "tab-00000" + string(rune('a'+i)), FPS: fps, HeapMB: 40, Mobile: i == 2}, now) {
			t.Fatal("report refused")
		}
	}
	if b.Add(Report{ID: "x", FPS: 60}, now) || b.Add(Report{ID: "tab-bad-fps", FPS: 5000}, now) {
		t.Error("implausible report kept")
	}
	s := b.Summary(now)
	if s.Count != 5 || s.MedianFPS != 58 || s.Mobile != 1 || len(s.Slow) != 1 || s.Slow[0].FPS != 12 {
		t.Errorf("summary %+v", s)
	}
	if s := b.Summary(now.Add(4 * time.Minute)); s.Count != 0 {
		t.Errorf("quiet tabs kept: %d", s.Count)
	}
}

func TestCheck(t *testing.T) {
	host := Host{CPUs: 2, MemTotal: 4 << 30, MemFree: 300 << 20}
	ws := Check(host, 1<<30, DirUsage{Free: 3 << 30, Size: 100 << 30}, Summary{CPU: 170, StepMs: 90, StepMaxMs: 140}, BrowserSummary{})
	text := ""
	for _, w := range ws {
		text += w.Level + ":" + w.Text + "\n"
		if w.Hint == "" {
			t.Errorf("no advice for %q", w.Text)
		}
	}
	for _, want := range []string{"bad:数据所在的磁盘只剩 3.0%", "bad:这台机器的内存用掉了 93%", "bad:仿真跟不上", "bad:SmartFactory 用满了机器的 CPU"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q in\n%s", want, text)
		}
	}
	if ws := Check(Host{CPUs: 8, MemTotal: 16 << 30, MemFree: 10 << 30}, 200<<20, DirUsage{Free: 50 << 30, Size: 100 << 30}, Summary{CPU: 3, StepMs: 2, StepMaxMs: 5}, BrowserSummary{}); len(ws) != 0 {
		t.Errorf("a healthy server warned: %+v", ws)
	}
}
