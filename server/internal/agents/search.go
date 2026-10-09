package agents

import (
	"slices"
	"strings"
	"unicode"
)

type piece struct{ heading, text string }

// chunk cuts a Markdown or plain-text document at its headings, and long sections at paragraph
// breaks, so a search returns a passage rather than a whole manual.
func chunk(body string) []piece {
	const most = 900
	var out []piece
	var path []string // heading trail: 1 曲轴 › 1.2 加热
	var buf strings.Builder
	flush := func() {
		t := strings.TrimSpace(buf.String())
		buf.Reset()
		if t == "" {
			return
		}
		h := strings.Join(path, " › ")
		for len([]rune(t)) > most {
			r := []rune(t)
			cut := most
			if i := strings.LastIndex(string(r[:most]), "\n\n"); i > most/3 {
				cut = len([]rune(string(r[:most])[:i]))
			}
			out = append(out, piece{h, strings.TrimSpace(string(r[:cut]))})
			t = strings.TrimSpace(string(r[cut:]))
		}
		if t != "" {
			out = append(out, piece{h, t})
		}
	}
	for line := range strings.SplitSeq(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		trim := strings.TrimSpace(line)
		if level := len(trim) - len(strings.TrimLeft(trim, "#")); level > 0 && level <= 4 && strings.HasPrefix(trim[level:], " ") {
			flush()
			if level-1 < len(path) {
				path = path[:level-1]
			}
			for len(path) < level-1 {
				path = append(path, "")
			}
			path = append(path, strings.TrimSpace(trim[level:]))
			path = slices.DeleteFunc(path, func(s string) bool { return s == "" })
			continue
		}
		buf.WriteString(line)
		buf.WriteByte('\n')
	}
	flush()
	if len(out) == 0 {
		out = append(out, piece{"", strings.TrimSpace(body)})
	}
	return out
}

// terms splits a query into words; a run of Chinese longer than two characters also yields its
// two-character pieces, since Chinese is written without spaces and the exact phrase may not be
// in the text.
func terms(q string) (whole, parts []string) {
	fields := strings.FieldsFunc(strings.ToLower(q), func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r) && r != '-' && r != '.' || unicode.IsSymbol(r)
	})
	for _, f := range fields {
		if !slices.Contains(whole, f) {
			whole = append(whole, f)
		}
		r := []rune(f)
		if len(r) > 2 && isHan(r) {
			for i := 0; i+2 <= len(r); i++ {
				if p := string(r[i : i+2]); !slices.Contains(parts, p) {
					parts = append(parts, p)
				}
			}
		}
	}
	return
}

func isHan(r []rune) bool {
	for _, c := range r {
		if !unicode.Is(unicode.Han, c) {
			return false
		}
	}
	return true
}

// rank scores each item by the query's words found in its text (whole words count more than
// two-character pieces) and returns the best limit with any match.
func rank[T any](items []T, query string, limit int, text func(T) string) []T {
	whole, parts := terms(query)
	if len(whole) == 0 {
		return items[:min(len(items), limit)]
	}
	type scored struct {
		item  T
		score float64
		order int
	}
	var hits []scored
	for i, it := range items {
		t := strings.ToLower(text(it))
		var sc float64
		for _, w := range whole {
			if n := strings.Count(t, w); n > 0 {
				sc += 3 + float64(min(n, 5))*0.3
			}
		}
		if len(parts) > 0 {
			found := 0
			for _, p := range parts {
				if strings.Contains(t, p) {
					found++
				}
			}
			sc += 2 * float64(found) / float64(len(parts))
		}
		if sc > 0 {
			hits = append(hits, scored{it, sc, i})
		}
	}
	slices.SortStableFunc(hits, func(a, b scored) int {
		switch {
		case a.score > b.score:
			return -1
		case a.score < b.score:
			return 1
		}
		return a.order - b.order
	})
	out := make([]T, 0, min(len(hits), limit))
	for _, h := range hits[:min(len(hits), limit)] {
		out = append(out, h.item)
	}
	return out
}
