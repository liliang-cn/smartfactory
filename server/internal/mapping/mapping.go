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
	"time"

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

// Future is how far ahead of the server's clock a device's timestamp may be: further, and the
// reading is taken to have arrived now (and flagged).
const Future = 5 * time.Minute

// ParseTime reads when a device took a reading: epoch milliseconds or seconds (a number or a
// numeric string), or RFC 3339.
func ParseTime(v any) (time.Time, bool) {
	var n float64
	switch x := v.(type) {
	case float64:
		n = x
	case json.Number:
		f, err := x.Float64()
		if err != nil {
			return time.Time{}, false
		}
		n = f
	case string:
		s := strings.TrimSpace(x)
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			n = f
			break
		}
		for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05.000", "2006-01-02 15:04:05"} {
			if t, err := time.Parse(layout, s); err == nil {
				return t, true
			}
		}
		return time.Time{}, false
	default:
		return time.Time{}, false
	}
	switch {
	case n <= 0:
		return time.Time{}, false
	case n > 1e14: // microseconds
		return time.UnixMicro(int64(n)), true
	case n > 1e11: // milliseconds
		return time.UnixMilli(int64(n)), true
	}
	sec, frac := math.Modf(n)
	return time.Unix(int64(sec), int64(frac*1e9)), true
}

// Stamp sets when a reading was taken from a device timestamp, keeping arrival time (and
// flagging the reading) when the timestamp is in the future.
func Stamp(t *tags.Tag, v any, now time.Time) {
	at, ok := ParseTime(v)
	if !ok {
		return
	}
	if at.After(now.Add(Future)) {
		t.At = now
		t.Q |= tags.FlagClock
		return
	}
	t.At = at
}

// timed splits {"v": x, "t": when} (or "value") into the value and its timestamp.
func timed(v any) (val any, ts any) {
	m, ok := v.(map[string]any)
	if !ok {
		return v, nil
	}
	for _, k := range []string{"v", "value"} {
		if x, ok := m[k]; ok {
			return x, firstOf(m, "t", "ts", "time")
		}
	}
	return v, nil
}

func firstOf(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return v
		}
	}
	return nil
}

// ExtractTimed is Extract plus when the reading was taken: from timePath when given, else from
// "t" beside "v"/"value" in an object payload; nil when the payload does not say.
func ExtractTimed(payload []byte, path, timePath string) (any, any, error) {
	if timePath != "" {
		v, err := Extract(payload, path)
		if err != nil {
			return nil, nil, err
		}
		ts, err := Extract(payload, timePath)
		if err != nil {
			return v, nil, nil
		}
		return v, ts, nil
	}
	if path == "" {
		s := strings.TrimSpace(string(payload))
		if strings.HasPrefix(s, "{") {
			var doc any
			if err := json.Unmarshal(payload, &doc); err != nil {
				return nil, nil, fmt.Errorf("payload is not JSON: %w", err)
			}
			v, ts := timed(doc)
			return v, ts, nil
		}
	}
	v, err := Extract(payload, path)
	return v, nil, err
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
// /cmd is a command the server itself publishes and is ignored. A reading carries the device's
// own time when the payload gives one: {"v": 61.5, "t": 1760000000000} on a field topic, a "t"
// (or "ts") member of an asset topic's object for all its fields, or {"v", "t"} as one field's value.
func Convention(prefix, topic string, payload []byte, src string) ([]tags.Tag, error) {
	return ConventionAt(prefix, topic, payload, src, time.Now())
}

// ConventionAt is Convention with the server's clock given.
func ConventionAt(prefix, topic string, payload []byte, src string, now time.Time) ([]tags.Tag, error) {
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
		v, ts, err := ExtractTimed(payload, "", "")
		if err != nil {
			return nil, err
		}
		t := tags.Tag{Company: c, Plant: p, Asset: a, Field: parts[4], Value: v, Source: src}
		Stamp(&t, ts, now)
		return []tags.Tag{t}, nil
	case 4:
		var m map[string]any
		if err := json.Unmarshal(payload, &m); err != nil {
			return nil, fmt.Errorf("%s: expected a JSON object of fields: %w", topic, err)
		}
		at := firstOf(m, "t", "ts")
		// alphabetical, so a fault's text arrives before the fault state and a reading before its counter
		fields := make([]string, 0, len(m))
		for f := range m {
			if f != "t" && f != "ts" {
				fields = append(fields, f)
			}
		}
		sort.Strings(fields)
		out := make([]tags.Tag, 0, len(m))
		for _, f := range fields {
			v, ts := timed(m[f])
			if ts == nil {
				ts = at
			}
			t := tags.Tag{Company: c, Plant: p, Asset: a, Field: f, Value: v, Source: src}
			Stamp(&t, ts, now)
			out = append(out, t)
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
