package mcptools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"smartfactory/internal/engine"
)

type simChange struct {
	Match   string   `json:"match" jsonschema:"asset id (HP-201), id prefix for every such machine of the lines (HP = every die press), or a line name"`
	Command string   `json:"command" jsonschema:"toollimit (planned die change point, arg 0.3–1 of the die life) or speed (arg 0.5–1.5)"`
	Arg     *float64 `json:"arg,omitempty" jsonschema:"the command's argument"`
}

type simIn struct {
	Company string      `json:"company" jsonschema:"company id"`
	Plant   string      `json:"plant" jsonschema:"plant id"`
	Lines   []string    `json:"lines,omitempty" jsonschema:"line names to simulate, e.g. 曲轴 2 线; keep it to the lines the plan touches (each extra line slows the run)"`
	Hours   float64     `json:"hours,omitempty" jsonschema:"how far ahead to simulate, 8–168 hours (default 72)"`
	Changes []simChange `json:"changes" jsonschema:"what the plan changes; empty runs the baseline only"`
	Seed    int         `json:"seed,omitempty" jsonschema:"random seed; the same seed gives the baseline and the plan the same luck"`
}

type simOut struct {
	Status string `json:"status" jsonschema:"done | running | failed"`
	Note   string `json:"note"`
	Result any    `json:"result,omitempty"`
}

// A what-if run takes minutes on a small server, longer than a tool call should block: the first
// call starts it, later calls with the same arguments pick up the result.
type simJob struct {
	done    chan struct{}
	started time.Time
	out     any
	err     error
}

var (
	simMu   sync.Mutex
	simJobs = map[string]*simJob{}
	simSlot = make(chan struct{}, 1) // one run at a time: they are CPU-bound
)

// under the 30 s a typical MCP client waits for a tool call
const simWait = 20 * time.Second

func addSimTool(s *mcp.Server, eng *engine.Engine) {
	mcp.AddTool(s, &mcp.Tool{Name: "simulate_plan", Description: "What-if on the twin: copies the plant's current state, runs the given lines ahead twice — as they are, and with the changes applied — " +
		"and returns both: output, first-pass yield, scrap rate, availability, OEE, tool changes and average hits per change, downtime by category, defects by type, " +
		"and spare stock (minimum, stock-out hours). Nothing on the live plant changes. A run takes a few minutes: if status is running, call again with the same " +
		"arguments later. Logistics are assumed to keep up; it predicts, the history database measures."},
		func(ctx context.Context, _ *mcp.CallToolRequest, in simIn) (*mcp.CallToolResult, simOut, error) {
			if in.Company == "" || in.Plant == "" {
				return nil, simOut{}, fmt.Errorf("company and plant are required")
			}
			if in.Hours == 0 {
				in.Hours = 72
			}
			in.Hours = min(max(in.Hours, 8), 168)
			if in.Seed == 0 {
				in.Seed = 7
			}
			for _, c := range in.Changes {
				if c.Command != "toollimit" && c.Command != "speed" {
					return nil, simOut{}, fmt.Errorf("change %q: command must be toollimit or speed", c.Match)
				}
			}
			key, _ := json.Marshal(in)
			sum := sha256.Sum256(key)
			id := hex.EncodeToString(sum[:8])

			simMu.Lock()
			for k, j := range simJobs {
				if time.Since(j.started) > time.Hour {
					delete(simJobs, k)
				}
			}
			j := simJobs[id]
			if j == nil {
				j = &simJob{done: make(chan struct{}), started: time.Now()}
				simJobs[id] = j
				state := eng.SaveJSON(in.Company)
				go j.run(eng, state, in)
			}
			simMu.Unlock()

			select {
			case <-j.done:
			case <-time.After(simWait):
			case <-ctx.Done():
			}
			select {
			case <-j.done:
				if j.err != nil {
					return nil, simOut{Status: "failed", Note: j.err.Error()}, nil
				}
				return nil, simOut{Status: "done", Note: fmt.Sprintf("模拟 %.0f 小时，用时 %s；同一随机种子下基线与方案对比", in.Hours, time.Since(j.started).Round(time.Second)), Result: j.out}, nil
			default:
				return nil, simOut{Status: "running", Note: fmt.Sprintf("推演进行中（已 %s），约 1–4 分钟完成；稍后用完全相同的参数再调用一次取结果", time.Since(j.started).Round(time.Second))}, nil
			}
		})
}

func (j *simJob) run(eng *engine.Engine, state string, in simIn) {
	defer close(j.done)
	simSlot <- struct{}{}
	defer func() { <-simSlot }()
	var raw string
	if node, err := exec.LookPath("node"); err == nil {
		// The same bundle in V8 runs about 20× faster than in the embedded engine.
		raw, err = runInNode(node, eng, state, in)
		if err != nil {
			j.err = err
			return
		}
	} else {
		off, err := engine.NewOffline(slog.Default())
		if err != nil {
			j.err = err
			return
		}
		off.Dir = eng.Dir
		off.LoadConfigs()
		var r struct{ Restored, Skipped []string }
		if err := json.Unmarshal([]byte(off.RestoreJSON(in.Company, state)), &r); err != nil || len(r.Restored) == 0 {
			j.err = fmt.Errorf("could not copy the plant's state (%v)", r.Skipped)
			return
		}
		raw = off.WhatIf(in.Company, in.Plant, in.Lines, in.Hours, 2, in.Changes, in.Seed)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		j.err = err
		return
	}
	if ok, _ := out["ok"].(bool); !ok {
		j.err = fmt.Errorf("%v", out["errors"])
		return
	}
	delete(out, "ok")
	j.out = out
}

// runner loads the bundle, the company's run-time configs (if it was created over MCP) and the
// live state, then runs the what-if; input on stdin, result on stdout.
const runner = `const fs = require('fs');
globalThis.host = { log: (m) => process.stderr.write(String(m) + '\n') };
(0, eval)(fs.readFileSync(process.argv[2], 'utf8'));
const inp = JSON.parse(fs.readFileSync(0, 'utf8'));
const SF = globalThis.SF;
SF.init();
for (const ind of inp.industries) SF.addIndustry(JSON.stringify(ind));
if (inp.companyConfig) SF.addCompany(JSON.stringify(inp.companyConfig));
const r = JSON.parse(SF.restore(inp.company, inp.state));
if (!r.restored.length) { process.stdout.write(JSON.stringify({ ok: false, errors: ['could not copy the plant state: ' + r.skipped.join(', ')] })); process.exit(0); }
process.stdout.write(SF.whatIf(inp.company, inp.plant, JSON.stringify(inp.lines || []), inp.hours, inp.dt, JSON.stringify(inp.changes || []), inp.seed));
`

var bundleOnce struct {
	sync.Once
	dir string
	err error
}

func runInNode(node string, eng *engine.Engine, state string, in simIn) (string, error) {
	bundleOnce.Do(func() {
		b, err := engine.Bundle()
		if err == nil {
			bundleOnce.dir, err = os.MkdirTemp("", "sf-whatif-")
		}
		if err == nil {
			err = os.WriteFile(filepath.Join(bundleOnce.dir, "engine.js"), b, 0o644)
		}
		if err == nil {
			err = os.WriteFile(filepath.Join(bundleOnce.dir, "run.cjs"), []byte(runner), 0o644)
		}
		bundleOnce.err = err
	})
	if bundleOnce.err != nil {
		return "", bundleOnce.err
	}
	input := map[string]any{
		"company": in.Company, "state": state, "plant": in.Plant, "lines": in.Lines, "hours": in.Hours, "dt": 2,
		"changes": in.Changes, "seed": in.Seed, "industries": []json.RawMessage{},
	}
	if company, inds, ok := eng.RuntimeCompany(in.Company); ok {
		list := []json.RawMessage{}
		for _, v := range inds {
			list = append(list, v)
		}
		input["industries"], input["companyConfig"] = list, company
	}
	body, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, node, filepath.Join(bundleOnce.dir, "run.cjs"), filepath.Join(bundleOnce.dir, "engine.js"))
	cmd.Stdin = bytes.NewReader(body)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("what-if in node: %v: %s", err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}
