package scoped

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

// Change is one edit of the config, the way the AI writes them: set a value (a key, or an
// element of a list), insert an element into a list (at an index, or "-" for the end), or remove
// a key or an element. Path is from the config's root: plants/0/lines/1/steps/3/cycle.
type Change struct {
	Op    string `json:"op"` // set | insert | remove
	Path  string `json:"path"`
	Value any    `json:"value,omitempty"`
}

// Apply makes the changes, in order, to a copy of config.
func Apply(config map[string]any, changes []Change) (map[string]any, error) {
	doc := clone(config)
	for i, c := range changes {
		p := strings.Split(strings.Trim(c.Path, "/ "), "/")
		if len(p) == 0 || p[0] == "" {
			return nil, fmt.Errorf("第 %d 处改动没有写 path", i+1)
		}
		var err error
		if doc, err = edit(doc, p, c); err != nil {
			return nil, fmt.Errorf("第 %d 处改动（%s %s）：%w", i+1, c.Op, c.Path, err)
		}
	}
	out, _ := doc.(map[string]any)
	return out, nil
}

// edit applies c at path p inside v and returns v changed.
func edit(v any, p []string, c Change) (any, error) {
	k := p[0]
	last := len(p) == 1
	switch t := v.(type) {
	case map[string]any:
		if last {
			switch c.Op {
			case "set":
				t[k] = clone(c.Value)
			case "remove":
				if _, ok := t[k]; !ok {
					return nil, fmt.Errorf("没有 %s", k)
				}
				delete(t, k)
			case "insert":
				list, ok := t[k].([]any)
				if !ok {
					return nil, fmt.Errorf("%s 不是列表，insert 要写到列表里的位置（…/%s/-）", k, k)
				}
				t[k] = append(list, clone(c.Value))
			default:
				return nil, fmt.Errorf("op 是 set、insert 或 remove，不是 %q", c.Op)
			}
			return t, nil
		}
		child, ok := t[k]
		if !ok {
			if c.Op != "set" {
				return nil, fmt.Errorf("没有 %s", k)
			}
			// setting thermal/warn on a step without thermal: make the object on the way
			child = map[string]any{}
		}
		nv, err := edit(child, p[1:], c)
		if err != nil {
			return nil, err
		}
		t[k] = nv
		return t, nil
	case []any:
		i, err := strconv.Atoi(k)
		if k == "-" {
			i, err = len(t), nil
		}
		if err != nil || i < 0 || i > len(t) || (i == len(t) && !(last && c.Op == "insert")) {
			return nil, fmt.Errorf("列表只有 %d 项，没有第 %s 项", len(t), k)
		}
		if last {
			switch c.Op {
			case "set":
				t[i] = clone(c.Value)
			case "insert":
				t = append(t[:i], append([]any{clone(c.Value)}, t[i:]...)...)
			case "remove":
				t = append(t[:i], t[i+1:]...)
			default:
				return nil, fmt.Errorf("op 是 set、insert 或 remove，不是 %q", c.Op)
			}
			return t, nil
		}
		nv, err := edit(t[i], p[1:], c)
		if err != nil {
			return nil, err
		}
		t[i] = nv
		return t, nil
	default:
		return nil, fmt.Errorf("%s 前面不是对象也不是列表", k)
	}
}

// Compact is config ready to keep: a line whose steps are still those of the routing they were
// copied from names the routing again, and the copies' $route marks are gone.
func Compact(m *Model, config map[string]any) map[string]any {
	out := obj(clone(config))
	for pi, p := range arr(out["plants"]) {
		ind := str(obj(p)["industry"])
		if ind == "" {
			ind = str(out["industry"])
		}
		if pi < len(m.Plants) {
			ind = m.Plants[pi].Industry
		}
		for _, l := range arr(obj(p)["lines"]) {
			lc := obj(l)
			r := str(lc["$route"])
			delete(lc, "$route")
			if r == "" {
				continue
			}
			if steps, ok := m.Routes[ind][r]; ok && reflect.DeepEqual(clone(steps), clone(lc["steps"])) {
				lc["route"] = r
				delete(lc, "steps")
			}
		}
	}
	return out
}
