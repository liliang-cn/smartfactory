// Package mapping translates protocol values into twin tags: topic matching with captures,
// JSON payload extraction, scaling and enumerations, and the default topic convention.
package mapping

import (
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"smartfactory/internal/config"
	"smartfactory/internal/tags"
)

// MatchTopic reports whether topic matches an MQTT filter and returns the segments matched by
// each "+" (and the remainder matched by "#") in order.
func MatchTopic(filter, topic string) ([]string, bool) {
	f := strings.Split(filter, "/")
	t := strings.Split(topic, "/")
	var caps []string
	for i, seg := range f {
		if seg == "#" {
			return append(caps, strings.Join(t[i:], "/")), true
		}
		if i >= len(t) {
			return nil, false
		}
		switch seg {
		case "+":
			caps = append(caps, t[i])
		case t[i]:
		default:
			return nil, false
		}
	}
	return caps, len(f) == len(t)
}

// Expand replaces {1}, {2}, … with captured topic segments.
func Expand(tpl string, caps []string) string {
	for i, c := range caps {
		tpl = strings.ReplaceAll(tpl, "{"+strconv.Itoa(i+1)+"}", c)
	}
	return tpl
}

// Extract reads a value from a payload: plain text numbers/booleans/strings, or a dotted path
// into JSON. A JSON object with "v" or "value" and no path yields that member.
func Extract(payload []byte, path string) (any, error) {
	s := strings.TrimSpace(string(payload))
	if path == "" {
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			return f, nil
		}
		if b, err := strconv.ParseBool(s); err == nil {
			return b, nil
		}
		if !strings.HasPrefix(s, "{") && !strings.HasPrefix(s, "[") {
			return strings.Trim(s, `"`), nil
		}
	}
	var doc any
	if err := json.Unmarshal(payload, &doc); err != nil {
		return nil, fmt.Errorf("payload is not JSON: %w", err)
	}
	if path == "" {
		if m, ok := doc.(map[string]any); ok {
			for _, k := range []string{"v", "value"} {
				if v, ok := m[k]; ok {
					return v, nil
				}
			}
		}
		return doc, nil
	}
	cur := doc
	for part := range strings.SplitSeq(path, ".") {
		switch node := cur.(type) {
		case map[string]any:
			v, ok := node[part]
			if !ok {
				return nil, fmt.Errorf("no %q in payload", path)
			}
			cur = v
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(node) {
				return nil, fmt.Errorf("bad index %q in %q", part, path)
			}
			cur = node[i]
		default:
			return nil, fmt.Errorf("no %q in payload", path)
		}
	}
	return cur, nil
}

// Convert applies an enumeration map, then scale and offset to numbers.
func Convert(v any, p config.Point) any {
	if len(p.Map) > 0 {
		key := fmt.Sprint(v)
		if f, ok := v.(float64); ok && f == math.Trunc(f) {
			key = strconv.FormatInt(int64(f), 10)
		}
		if m, ok := p.Map[key]; ok {
			return m
		}
	}
	f, ok := toFloat(v)
	if !ok || (p.Scale == nil && p.Offset == nil) {
		return v
	}
	if p.Scale != nil {
		f *= *p.Scale
	}
	if p.Offset != nil {
		f += *p.Offset
	}
	return math.Round(f*1e6) / 1e6
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int16:
		return float64(n), true
	case int32:
		return float64(n), true
	case int64:
		return float64(n), true
	case uint16:
		return float64(n), true
	case uint32:
		return float64(n), true
	case uint64:
		return float64(n), true
	}
	return 0, false
}

// Convention parses {prefix}/{company}/{plant}/{asset}[/{field}] topics. A topic ending in
// /cmd is a command the server itself publishes and is ignored.
func Convention(prefix, topic string, payload []byte, src string) ([]tags.Tag, error) {
	parts := strings.Split(topic, "/")
	if len(parts) < 4 || parts[0] != prefix {
		return nil, nil
	}
	c, p, a := parts[1], parts[2], parts[3]
	switch len(parts) {
	case 5:
		if parts[4] == "cmd" {
			return nil, nil
		}
		v, err := Extract(payload, "")
		if err != nil {
			return nil, err
		}
		return []tags.Tag{{Company: c, Plant: p, Asset: a, Field: parts[4], Value: v, Source: src}}, nil
	case 4:
		var m map[string]any
		if err := json.Unmarshal(payload, &m); err != nil {
			return nil, fmt.Errorf("%s: expected a JSON object of fields: %w", topic, err)
		}
		// alphabetical, so a fault's text arrives before the fault state and a reading before its counter
		fields := make([]string, 0, len(m))
		for f := range m {
			fields = append(fields, f)
		}
		sort.Strings(fields)
		out := make([]tags.Tag, 0, len(m))
		for _, f := range fields {
			out = append(out, tags.Tag{Company: c, Plant: p, Asset: a, Field: f, Value: m[f], Source: src})
		}
		return out, nil
	}
	return nil, nil
}

// CommandTopic is where the server publishes operator commands under the convention.
func CommandTopic(prefix string, c tags.Command) string {
	return strings.Join([]string{prefix, c.Company, c.Plant, c.Asset, "cmd"}, "/")
}

// CommandMatches reports whether a binding applies to a command.
func CommandMatches(b config.CmdBinding, c tags.Command) bool {
	return (b.Company == "" || b.Company == c.Company) && (b.Plant == "" || b.Plant == c.Plant) &&
		b.Asset == c.Asset && (b.Cmd == "*" || b.Cmd == c.Cmd)
}
