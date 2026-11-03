package engine

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Earlier versions of a company's config: each time a running company is replaced (a save from
// the builder or the scoped editor), the config it ran before is kept, the last keepVersions of
// them, so a change can be undone. They live in <Dir>/configs/versions/<company>/<unix ns>.json.

const keepVersions = 20

// Version is one earlier config of a company.
type Version struct {
	At time.Time `json:"at"`
	// file is its name in the company's versions folder; raw, without a Dir, the config itself
	file string
	raw  []byte
}

func (e *Engine) versionDir(id string) string { return filepath.Join(e.Dir, "configs", "versions", id) }

// keepVersion keeps raw, the config company id ran until now.
func (e *Engine) keepVersion(id string, raw []byte) {
	now := time.Now()
	if e.Dir == "" {
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.memVersions == nil {
			e.memVersions = map[string][]Version{}
		}
		v := append(e.memVersions[id], Version{At: now, raw: raw})
		if len(v) > keepVersions {
			v = v[len(v)-keepVersions:]
		}
		e.memVersions[id] = v
		return
	}
	dir := e.versionDir(id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		e.log.Error("keep version", "err", err)
		return
	}
	if err := os.WriteFile(filepath.Join(dir, strconv.FormatInt(now.UnixNano(), 10)+".json"), raw, 0o644); err != nil {
		e.log.Error("keep version", "err", err)
	}
	vs := e.Versions(id)
	for len(vs) > keepVersions {
		os.Remove(filepath.Join(dir, vs[0].file))
		vs = vs[1:]
	}
}

// Versions are the company's earlier configs, oldest first.
func (e *Engine) Versions(id string) []Version {
	if e.Dir == "" {
		e.mu.Lock()
		defer e.mu.Unlock()
		return slices.Clone(e.memVersions[id])
	}
	entries, _ := os.ReadDir(e.versionDir(id))
	var out []Version
	for _, f := range entries {
		ns, err := strconv.ParseInt(strings.TrimSuffix(f.Name(), ".json"), 10, 64)
		if err != nil || f.IsDir() {
			continue
		}
		out = append(out, Version{At: time.Unix(0, ns), file: f.Name()})
	}
	slices.SortFunc(out, func(a, b Version) int { return a.At.Compare(b.At) })
	return out
}

// Undo puts company id back on the config it ran before its last replacement, and forgets that
// version (undoing again goes one further back). Not OK when there is none.
func (e *Engine) Undo(id string) ConfigResult {
	if r, bad := badID(id); bad {
		return r
	}
	vs := e.Versions(id)
	if len(vs) == 0 {
		return ConfigResult{Errors: []string{"没有更早的版本可以撤销"}}
	}
	last := vs[len(vs)-1]
	raw := last.raw
	if e.Dir != "" {
		var err error
		if raw, err = os.ReadFile(filepath.Join(e.versionDir(id), last.file)); err != nil {
			return ConfigResult{Errors: []string{err.Error()}}
		}
	}
	r := e.install(raw, false)
	if r.OK {
		if e.Dir == "" {
			e.mu.Lock()
			e.memVersions[id] = e.memVersions[id][:len(e.memVersions[id])-1]
			e.mu.Unlock()
		} else {
			os.Remove(filepath.Join(e.versionDir(id), last.file))
		}
	}
	return r
}
