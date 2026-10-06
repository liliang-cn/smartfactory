// Package engine runs the twin's simulation on the server: the same TypeScript the browser
// uses (src/server/engine.ts, bundled into js/engine.js) inside an embedded JS engine.
// Every company's plants advance in real time whether or not anyone is watching; browsers
// follow it through frames, MCP reads it, protocol adapters feed it.
package engine

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"

	"smartfactory/internal/sfconv"
)

//go:embed all:js
var files embed.FS

// The JS runtime is single-threaded: every call goes through the loop goroutine.
type Engine struct {
	vm    *goja.Runtime
	fn    map[string]goja.Callable
	calls chan func()
	log   *slog.Logger

	companies []string // guarded by mu

	mu       sync.Mutex
	watchers map[chan []byte]*watch
	ticks    int
	stepCost atomic.Int64 // ns, last step

	// Dir is where company state is saved (every minute and on shutdown); empty disables it.
	Dir     string
	savedAt time.Time
	// Done is closed once Run has returned and the final save is on disk.
	Done chan struct{}
}

// Result is what a command returns.
type Result struct {
	OK  bool   `json:"ok"`
	Msg string `json:"msg"`
}

func New(log *slog.Logger) (*Engine, error) {
	src, err := files.ReadFile("js/engine.js")
	if err != nil {
		return nil, errors.New("engine bundle missing: run `npm run build:engine` (scripts/build.sh does)")
	}
	vm := goja.New()
	jsLog := func(level slog.Level) func(goja.FunctionCall) goja.Value {
		return func(c goja.FunctionCall) goja.Value {
			args := make([]any, len(c.Arguments))
			for i, a := range c.Arguments {
				args[i] = a.String()
			}
			log.Log(context.Background(), level, "engine", "msg", fmt.Sprint(args...))
			return goja.Undefined()
		}
	}
	console := vm.NewObject()
	console.Set("log", jsLog(slog.LevelInfo))
	console.Set("info", jsLog(slog.LevelInfo))
	console.Set("warn", jsLog(slog.LevelWarn))
	console.Set("error", jsLog(slog.LevelError))
	vm.Set("console", console)
	host := vm.NewObject()
	host.Set("log", jsLog(slog.LevelWarn))
	vm.Set("host", host)

	if _, err := vm.RunScript("engine.js", string(src)); err != nil {
		return nil, fmt.Errorf("load engine: %w", err)
	}
	sf := vm.Get("SF")
	if sf == nil || goja.IsUndefined(sf) {
		return nil, errors.New("engine bundle does not define SF")
	}
	obj := sf.ToObject(vm)
	e := &Engine{vm: vm, fn: map[string]goja.Callable{}, calls: make(chan func(), 256), log: log, watchers: map[chan []byte]*watch{}, Done: make(chan struct{})}
	for _, name := range []string{"init", "step", "frame", "plantFrame", "timeScale", "snapshot", "command", "ingest", "hist", "setTimeScale", "list", "save", "restore", "machines", "playEquipment", "telemetry", "addIndustry", "addCompany", "removeCompany", "industryList", "industry", "schema"} {
		f, ok := goja.AssertFunction(obj.Get(name))
		if !ok {
			return nil, fmt.Errorf("engine: SF.%s is not a function", name)
		}
		e.fn[name] = f
	}
	v, err := e.fn["init"](goja.Undefined())
	if err != nil {
		return nil, fmt.Errorf("engine init: %w", err)
	}
	if err := json.Unmarshal([]byte(v.String()), &e.companies); err != nil {
		return nil, err
	}
	return e, nil
}

func (e *Engine) call(name string, args ...any) (goja.Value, error) {
	vals := make([]goja.Value, len(args))
	for i, a := range args {
		vals[i] = e.vm.ToValue(a)
	}
	return e.fn[name](goja.Undefined(), vals...)
}

// do runs fn on the loop goroutine and waits for it.
func (e *Engine) do(fn func()) {
	done := make(chan struct{})
	e.calls <- func() { fn(); close(done) }
	<-done
}

func (e *Engine) str(name string, args ...any) string {
	var out string
	e.do(func() {
		v, err := e.call(name, args...)
		if err != nil {
			e.log.Error("engine call", "fn", name, "err", err)
			out = "null"
			return
		}
		out = v.String()
	})
	return out
}

// StepCost is how long the last simulation step took.
func (e *Engine) StepCost() time.Duration { return time.Duration(e.stepCost.Load()) }

// SavedAt is when state was last written to disk.
func (e *Engine) SavedAt() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.savedAt
}

// Restore carries every company on from its saved state, if any. Call before Run.
func (e *Engine) Restore() {
	if e.Dir == "" {
		return
	}
	for _, c := range e.CompanyIDs() {
		b, err := os.ReadFile(filepath.Join(e.Dir, c+".json"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			e.log.Warn("read saved state", "company", c, "err", err)
			continue
		}
		v, err := e.call("restore", c, string(b))
		if err != nil {
			e.log.Error("restore", "company", c, "err", err)
			continue
		}
		var r struct{ Restored, Skipped []string }
		json.Unmarshal([]byte(v.String()), &r)
		e.log.Info("state restored", "company", c, "plants", r.Restored, "fresh", r.Skipped)
	}
}

// save serialises every company on the loop goroutine and writes the files, in the
// background unless wait is set (shutdown).
func (e *Engine) save(wait bool) {
	if e.Dir == "" {
		return
	}
	data := map[string]string{}
	for _, c := range e.CompanyIDs() {
		v, err := e.call("save", c)
		if err != nil {
			e.log.Error("save", "company", c, "err", err)
			continue
		}
		data[c] = v.String()
	}
	write := func() {
		if err := os.MkdirAll(e.Dir, 0o755); err != nil {
			e.log.Error("state dir", "err", err)
			return
		}
		for c, js := range data {
			path := filepath.Join(e.Dir, c+".json")
			tmp := path + ".tmp"
			if err := os.WriteFile(tmp, []byte(js), 0o644); err != nil {
				e.log.Error("write state", "company", c, "err", err)
				continue
			}
			if err := os.Rename(tmp, path); err != nil {
				e.log.Error("write state", "company", c, "err", err)
			}
		}
		e.mu.Lock()
		e.savedAt = time.Now()
		e.mu.Unlock()
	}
	if wait {
		write()
	} else {
		go write()
	}
}

// Run advances every plant in real time (10 Hz), sends frames to watchers (2 Hz) and saves
// state every minute and when ctx ends.
func (e *Engine) Run(ctx context.Context) {
	defer close(e.Done)
	tick := time.NewTicker(100 * time.Millisecond)
	defer tick.Stop()
	persist := time.NewTicker(time.Minute)
	defer persist.Stop()
	last := time.Now()
	n := 0
	for {
		select {
		case <-ctx.Done():
			e.save(true)
			e.log.Info("state saved", "dir", e.Dir)
			return
		case <-persist.C:
			e.save(false)
		case fn := <-e.calls:
			fn()
		case now := <-tick.C:
			dt := min(now.Sub(last).Seconds(), 0.5)
			last = now
			t0 := time.Now()
			if _, err := e.call("step", dt, e.watched()); err != nil {
				e.log.Error("engine step", "err", err)
			}
			e.stepCost.Store(int64(time.Since(t0)))
			if n++; n%5 == 0 {
				e.broadcast()
			}
		}
	}
}

type watch struct {
	company, plant string
	// send every plant in the next frame (new watcher, plant switch)
	full bool
}

// Plants are replicated at 2 Hz for the plant a browser is looking at, and every 4 s for the
// company's other plants (enough for the plant switcher and cross-plant alarms).
const fullEvery = 8

func (e *Engine) broadcast() {
	e.mu.Lock()
	e.ticks++
	periodic := e.ticks%fullEvery == 0
	type target struct {
		ch chan []byte
		w  watch
	}
	var targets []target
	need := map[string]map[string]bool{} // company → plants to export ("*" = all)
	for ch, w := range e.watchers {
		targets = append(targets, target{ch, *w})
		if need[w.company] == nil {
			need[w.company] = map[string]bool{}
		}
		if periodic || w.full {
			need[w.company]["*"] = true
		}
		need[w.company][w.plant] = true
		w.full = false
	}
	e.mu.Unlock()

	frames := map[string]map[string]string{}
	scale := map[string]string{}
	for c, plants := range need {
		frames[c] = map[string]string{}
		ids := []string{}
		if plants["*"] {
			var list []struct {
				ID     string `json:"id"`
				Plants []struct {
					ID string `json:"id"`
				} `json:"plants"`
			}
			if v, err := e.call("list"); err == nil && json.Unmarshal([]byte(v.String()), &list) == nil {
				for _, co := range list {
					if co.ID == c {
						for _, p := range co.Plants {
							ids = append(ids, p.ID)
						}
					}
				}
			}
		} else {
			for p := range plants {
				ids = append(ids, p)
			}
		}
		for _, p := range ids {
			if v, err := e.call("plantFrame", c, p); err == nil && v.String() != "null" {
				frames[c][p] = v.String()
			}
		}
		if v, err := e.call("timeScale", c); err == nil {
			scale[c] = v.String()
		}
	}

	for _, t := range targets {
		fs := frames[t.w.company]
		if len(fs) == 0 {
			continue
		}
		var b strings.Builder
		b.WriteString(`{"type":"frame","data":{"company":`)
		cj, _ := json.Marshal(t.w.company)
		b.Write(cj)
		b.WriteString(`,"timeScale":` + scale[t.w.company] + `,"plants":{`)
		first := true
		for p, js := range fs {
			// everything when this watcher asked for it or on the periodic refresh; else its plant
			if !(periodic || t.w.full || p == t.w.plant) {
				continue
			}
			if !first {
				b.WriteByte(',')
			}
			first = false
			pj, _ := json.Marshal(p)
			b.Write(pj)
			b.WriteByte(':')
			b.WriteString(js)
		}
		b.WriteString(`}}}`)
		select {
		case t.ch <- []byte(b.String()):
		default: // a slow client skips a frame; the next one carries current state anyway
		}
	}
}

// watched lists the companies someone is following, comma-separated.
func (e *Engine) watched() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	seen := map[string]bool{}
	var out []string
	for _, w := range e.watchers {
		if !seen[w.company] {
			seen[w.company] = true
			out = append(out, w.company)
		}
	}
	return strings.Join(out, ",")
}

// Watch subscribes to a company's frames, starting with every plant.
func (e *Engine) Watch(company, plant string) (<-chan []byte, func(plant string), func()) {
	ch := make(chan []byte, 4)
	e.mu.Lock()
	e.watchers[ch] = &watch{company: company, plant: plant, full: true}
	e.mu.Unlock()
	setPlant := func(p string) {
		e.mu.Lock()
		if w := e.watchers[ch]; w != nil && w.plant != p {
			w.plant, w.full = p, true
		}
		e.mu.Unlock()
	}
	return ch, setPlant, func() {
		e.mu.Lock()
		delete(e.watchers, ch)
		e.mu.Unlock()
	}
}

// Frame is the current replicated state of a company (JSON), or "null".
func (e *Engine) Frame(company string) string { return e.str("frame", company) }

// Snapshot is a compact view for MCP tools (JSON), or "null".
func (e *Engine) Snapshot(company string) string { return e.str("snapshot", company) }

// List returns companies and their plants (JSON).
func (e *Engine) List() string { return e.str("list") }

// Hist returns an asset's trend buffers (JSON), or "null".
func (e *Engine) Hist(company, plant, asset string) string {
	return e.str("hist", company, plant, asset)
}

func (e *Engine) Command(company, plant, asset, cmd string, arg *float64) Result {
	var a any
	if arg != nil {
		a = *arg
	}
	var r Result
	if err := json.Unmarshal([]byte(e.str("command", company, plant, asset, cmd, a)), &r); err != nil {
		return Result{Msg: err.Error()}
	}
	return r
}

// Ingest queues a measured value without waiting.
func (e *Engine) Ingest(company, plant, asset, field string, value any, src string) {
	switch v := value.(type) {
	case float64, string, bool:
	case float32:
		value = float64(v)
	default:
		value = fmt.Sprint(v)
	}
	select {
	case e.calls <- func() {
		if _, err := e.call("ingest", company, plant, asset, field, value, src); err != nil {
			e.log.Warn("engine ingest", "err", err)
		}
	}:
	default:
		e.log.Warn("engine busy, dropped a value", "asset", asset, "field", field)
	}
}

func (e *Engine) SetTimeScale(company string, scale float64) bool {
	return e.str("setTimeScale", company, scale) == "true"
}

// Machines lists a plant's machines in the order the protocol conventions use.
func (e *Engine) Machines(company, plant string) []sfconv.Machine {
	var out []sfconv.Machine
	json.Unmarshal([]byte(e.str("machines", company, plant)), &out)
	return out
}

// PlayEquipment switches every plant into device-emulator mode (see the devices package).
func (e *Engine) PlayEquipment() { e.str("playEquipment") }

// MachineState is what one machine reports.
type MachineState struct {
	ID          string  `json:"id"`
	State       string  `json:"state"`
	Fault       string  `json:"fault"`
	FaultCode   int     `json:"faultCode"`
	Temp        float64 `json:"temp"`
	Power       float64 `json:"power"`
	Vib         float64 `json:"vib"`
	Speed       float64 `json:"speed"`
	Health      float64 `json:"health"`
	MCount      int     `json:"mcount"`
	LastMeasure float64 `json:"lastMeasure"`
	Measured    bool    `json:"measured"`
}

// Telemetry is a plant's equipment state plus units measured since the last call.
type Telemetry struct {
	Machines []MachineState `json:"machines"`
	Readings [][2]any       `json:"readings"`
}

func (e *Engine) Telemetry(company, plant string) (Telemetry, error) {
	var t Telemetry
	err := json.Unmarshal([]byte(e.str("telemetry", company, plant)), &t)
	return t, err
}

// CompanyIDs lists the companies being simulated.
func (e *Engine) CompanyIDs() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.companies...)
}

// ---------- configs created at run time (MCP) ----------
//
// Industry packs and companies submitted by clients are validated by the same schemas as the
// bundled ones, hot-loaded into the simulation, and kept as files under {Dir}/configs so they
// come back after a restart.

// ConfigResult is the engine's verdict on a submitted config.
type ConfigResult struct {
	OK     bool            `json:"ok"`
	Errors []string        `json:"errors,omitempty"`
	Where  string          `json:"where,omitempty"`
	Detail json.RawMessage `json:"detail,omitempty"`
}

func (e *Engine) configDir(kind string) string { return filepath.Join(e.Dir, "configs", kind) }

func (e *Engine) verdict(js string) ConfigResult {
	var r ConfigResult
	if err := json.Unmarshal([]byte(js), &r); err != nil {
		return ConfigResult{Errors: []string{err.Error()}}
	}
	r.Detail = json.RawMessage(js)
	return r
}

func (e *Engine) persistConfig(kind, id string, raw []byte) error {
	if e.Dir == "" {
		return nil
	}
	dir := e.configDir(kind)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, id+".json"), raw, 0o644)
}

func configID(raw []byte) string {
	var v struct {
		ID string `json:"id"`
	}
	json.Unmarshal(raw, &v)
	return v.ID
}

// CreateIndustry validates and loads an industry pack, and keeps it.
func (e *Engine) CreateIndustry(raw []byte) ConfigResult {
	r := e.verdict(e.str("addIndustry", string(raw)))
	if r.OK {
		if err := e.persistConfig("industries", configID(raw), raw); err != nil {
			e.log.Error("keep industry", "err", err)
		}
	}
	return r
}

// CreateCompany validates, lays out and starts simulating a company, and keeps its config.
func (e *Engine) CreateCompany(raw []byte) ConfigResult {
	r := e.verdict(e.str("addCompany", string(raw)))
	if r.OK {
		id := configID(raw)
		e.mu.Lock()
		if !slices.Contains(e.companies, id) {
			e.companies = append(e.companies, id)
		}
		e.mu.Unlock()
		if err := e.persistConfig("companies", id, raw); err != nil {
			e.log.Error("keep company", "err", err)
		}
	}
	return r
}

// DeleteCompany stops simulating a company created at run time and forgets it.
func (e *Engine) DeleteCompany(id string) ConfigResult {
	r := e.verdict(e.str("removeCompany", id))
	if r.OK {
		e.mu.Lock()
		e.companies = slices.DeleteFunc(e.companies, func(c string) bool { return c == id })
		e.mu.Unlock()
		if e.Dir != "" {
			os.Remove(filepath.Join(e.configDir("companies"), id+".json"))
			os.Remove(filepath.Join(e.Dir, id+".json"))
		}
	}
	return r
}

// LoadConfigs brings back what was created at run time. Call before Restore and Run.
func (e *Engine) LoadConfigs() {
	if e.Dir == "" {
		return
	}
	for _, kind := range []string{"industries", "companies"} {
		files, _ := filepath.Glob(filepath.Join(e.configDir(kind), "*.json"))
		for _, f := range files {
			raw, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			fn := "addIndustry"
			if kind == "companies" {
				fn = "addCompany"
			}
			v, err := e.call(fn, string(raw))
			if err != nil {
				e.log.Error("load config", "file", f, "err", err)
				continue
			}
			r := e.verdict(v.String())
			if !r.OK {
				e.log.Error("config no longer valid", "file", f, "errors", r.Errors)
				continue
			}
			if kind == "companies" {
				e.companies = append(e.companies, configID(raw))
			}
			e.log.Info("config loaded", "kind", kind, "id", configID(raw))
		}
	}
}

// RuntimeCompany returns a run-time company's config and the run-time industry packs it uses,
// for browsers (bundled packs they already have).
func (e *Engine) RuntimeCompany(id string) (company json.RawMessage, industries map[string]json.RawMessage, ok bool) {
	if e.Dir == "" {
		return nil, nil, false
	}
	raw, err := os.ReadFile(filepath.Join(e.configDir("companies"), filepath.Base(id)+".json"))
	if err != nil {
		return nil, nil, false
	}
	var cfg struct {
		Industry string `json:"industry"`
		Plants   []struct {
			Industry string `json:"industry"`
		} `json:"plants"`
	}
	json.Unmarshal(raw, &cfg)
	industries = map[string]json.RawMessage{}
	for _, ind := range append([]string{cfg.Industry}, func() (out []string) {
		for _, p := range cfg.Plants {
			out = append(out, p.Industry)
		}
		return
	}()...) {
		if ind == "" {
			continue
		}
		if b, err := os.ReadFile(filepath.Join(e.configDir("industries"), filepath.Base(ind)+".json")); err == nil {
			industries[ind] = b
		}
	}
	return raw, industries, true
}

// RuntimeDirectory lists companies created at run time, for the company switcher.
func (e *Engine) RuntimeDirectory() []map[string]string {
	out := []map[string]string{}
	if e.Dir == "" {
		return out
	}
	files, _ := filepath.Glob(filepath.Join(e.configDir("companies"), "*.json"))
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var c struct{ ID, Name, Short string }
		if json.Unmarshal(raw, &c) == nil && c.ID != "" {
			out = append(out, map[string]string{"id": c.ID, "name": c.Name, "short": c.Short})
		}
	}
	return out
}

func (e *Engine) IndustryList() string      { return e.str("industryList") }
func (e *Engine) Industry(id string) string { return e.str("industry", id) }
func (e *Engine) Schema(kind string) string { return e.str("schema", kind) }
