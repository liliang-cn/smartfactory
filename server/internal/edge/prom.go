package edge

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Series is one sample of a Prometheus text exposition.
type Series struct {
	Name   string
	Labels map[string]string
	Value  float64
}

// Scrape fetches and parses a Prometheus/OpenMetrics text endpoint (node_exporter, etc.).
func Scrape(ctx context.Context, url string) ([]Series, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	return Parse(resp.Body)
}

// Parse reads the text exposition format: `name{label="value",...} value [timestamp]`.
func Parse(r io.Reader) ([]Series, error) {
	var out []Series
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		s := Series{Labels: map[string]string{}}
		rest := line
		if i := strings.IndexAny(line, "{ "); i >= 0 && line[i] == '{' {
			s.Name = line[:i]
			end, err := parseLabels(line[i+1:], s.Labels)
			if err != nil {
				return nil, fmt.Errorf("%q: %w", line, err)
			}
			rest = strings.TrimSpace(line[i+1+end:])
		} else if i >= 0 {
			s.Name = line[:i]
			rest = strings.TrimSpace(line[i:])
		} else {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			continue
		}
		v, err := strconv.ParseFloat(fields[0], 64)
		if err != nil {
			continue
		}
		s.Value = v
		out = append(out, s)
	}
	return out, sc.Err()
}

// parseLabels parses `a="x",b="y"}` and returns the index just past the closing brace.
func parseLabels(s string, into map[string]string) (int, error) {
	i := 0
	for i < len(s) {
		for i < len(s) && (s[i] == ' ' || s[i] == ',') {
			i++
		}
		if i < len(s) && s[i] == '}' {
			return i + 1, nil
		}
		eq := strings.IndexByte(s[i:], '=')
		if eq < 0 || i+eq+1 >= len(s) || s[i+eq+1] != '"' {
			return 0, fmt.Errorf("bad label at %d", i)
		}
		key := strings.TrimSpace(s[i : i+eq])
		i += eq + 2
		var b strings.Builder
		for i < len(s) && s[i] != '"' {
			if s[i] == '\\' && i+1 < len(s) {
				i++
				switch s[i] {
				case 'n':
					b.WriteByte('\n')
				default:
					b.WriteByte(s[i])
				}
			} else {
				b.WriteByte(s[i])
			}
			i++
		}
		if i >= len(s) {
			return 0, fmt.Errorf("unterminated label value")
		}
		into[key] = b.String()
		i++
	}
	return 0, fmt.Errorf("missing }")
}

// Selector picks series by name and labels and folds them into one number.
type Selector struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels"`
	// avg (default) | sum | max | min
	Agg string `json:"agg"`
}

func (sel Selector) Eval(all []Series) (float64, bool) {
	var vals []float64
next:
	for _, s := range all {
		if s.Name != sel.Name {
			continue
		}
		for k, v := range sel.Labels {
			if s.Labels[k] != v {
				continue next
			}
		}
		vals = append(vals, s.Value)
	}
	if len(vals) == 0 {
		return 0, false
	}
	r := vals[0]
	switch sel.Agg {
	case "sum":
		r = 0
		for _, v := range vals {
			r += v
		}
	case "max":
		for _, v := range vals {
			r = max(r, v)
		}
	case "min":
		for _, v := range vals {
			r = min(r, v)
		}
	default:
		r = 0
		for _, v := range vals {
			r += v
		}
		r /= float64(len(vals))
	}
	return r, true
}
