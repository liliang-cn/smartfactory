package knowledge

import (
	"regexp"
	"sort"
	"strings"
	"unicode"
)

// How a document's passage is linked to the process graph. Names are found the way people write
// them: "IH101", "ih-101" and "IH 101" are station IH-101, "曲轴1线" is 曲轴 1 线, a part
// number names its product, and a machine code with a number or series after it ("HP-1xx",
// "HP 系列") names the machine. Identifiers match whole (HP-101 is not in HP-1012), and nothing is
// linked by a single character.

// term is one way a document can name a thing in the graph.
type term struct {
	key  string // what is looked for, normalised
	name string // the thing it names in the graph
	typ  string
	// id: letters and digits, matched only as a whole word
	id bool
	// code: a machine code, matched only with a number or series after it
	code *regexp.Regexp
}

// norm is s as matched: ASCII letters in upper case, without spaces, hyphens and underscores.
func norm(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case unicode.IsSpace(r), strings.ContainsRune("-_‐‑‒–—－＿·", r):
			continue
		case r < 0x80:
			b.WriteRune(unicode.ToUpper(r))
		case r >= 'Ａ' && r <= 'Ｚ', r >= 'ａ' && r <= 'ｚ', r >= '０' && r <= '９':
			// full-width letters and digits, as Chinese typing produces them
			b.WriteRune(unicode.ToUpper(r - 0xFEE0))
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func ascii(r rune) bool { return r < 0x80 && (unicode.IsLetter(r) || unicode.IsDigit(r)) }

// identifier says whether a name is a code of letters and digits (a station number, a part
// number) rather than words.
func identifier(s string) bool {
	letters, digits := false, false
	for _, r := range s {
		switch {
		case r < 0x80 && unicode.IsLetter(r):
			letters = true
		case unicode.IsDigit(r):
			digits = true
		case r >= 0x80:
			return false
		}
	}
	return letters && digits
}

// terms are the ways the model's things are named, longest first.
func terms(m *Model) []term {
	n := nameModel(m)
	var out []term
	seen := map[string]bool{}
	add := func(written, name, typ string) {
		key := norm(written)
		if len([]rune(key)) < 2 || len([]rune(name)) < 2 || seen[key+"\x00"+name] {
			return
		}
		seen[key+"\x00"+name] = true
		out = append(out, term{key: key, name: name, typ: typ, id: identifier(key)})
	}
	lineNames := map[string][]string{} // a line's own name → the names it has in the graph
	codes := map[string]string{}       // machine code → machine
	for _, p := range m.Plants {
		add(p.Name, p.Name, TPlant)
		for _, l := range p.Lines {
			full := n.line[p.ID+"/"+l.ID]
			add(full, full, TLine)
			lineNames[l.Name] = append(lineNames[l.Name], full)
			add(l.Product.Name, l.Product.Name, TProduct)
			for _, pn := range uniq(append([]string{l.Product.PartNo}, l.Product.Variants...)) {
				if pn != "" && len([]rune(norm(pn))) >= 3 {
					add(pn, l.Product.Name, TProduct)
				}
			}
			for _, s := range uniq(l.Product.Suppliers) {
				add(s, s, TSupplier)
			}
			for _, c := range uniq(l.Product.Customers) {
				add(c, c, TCustomer)
			}
			for _, s := range l.Stations {
				st := n.station[p.ID+"/"+s.ID]
				add(st, st, TStation)
				if st != s.ID && stationsNamed(n, s.ID) == 1 {
					add(s.ID, st, TStation)
				}
				add(s.Label, s.Label, TMachine)
				if s.Code != "" {
					codes[s.Code] = s.Label
				}
			}
		}
	}
	// a line's own name where it names one line only ("曲轴 1 线" in a company with one such line)
	for own, full := range lineNames {
		if len(full) == 1 && own != full[0] {
			add(own, full[0], TLine)
		}
	}
	add(m.Name, m.Name, TCompany)
	for code, machine := range codes {
		if len([]rune(code)) < 2 {
			continue
		}
		re := regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9])` + regexp.QuoteMeta(code) + `(?:[\s\-_]*(?:\d|[xX×*]{1,3}(?:[^A-Za-z]|$))|\s*(?:系列|型|类|机组))`)
		out = append(out, term{key: norm(code), name: machine, typ: TMachine, code: re})
	}
	sort.SliceStable(out, func(i, j int) bool { return len([]rune(out[i].key)) > len([]rune(out[j].key)) })
	return out
}

func stationsNamed(n names, id string) int {
	c := 0
	for k := range n.station {
		if k[strings.IndexByte(k, '/')+1:] == id {
			c++
		}
	}
	return c
}

// link is the things of the graph a passage names, each once.
func link(vocab []term, text string) []term {
	key := norm(text)
	var out []term
	seen := map[string]bool{}
	for _, t := range vocab {
		if seen[t.name+"\x00"+t.typ] {
			continue
		}
		var hit bool
		switch {
		case t.code != nil:
			hit = t.code.MatchString(text)
		case t.id:
			hit = wholeIn(key, t.key)
		default:
			hit = strings.Contains(key, t.key)
		}
		if hit {
			seen[t.name+"\x00"+t.typ] = true
			out = append(out, t)
		}
	}
	return out
}

// wholeIn says whether word is in s with no letter or digit right before or after it.
func wholeIn(s, word string) bool {
	for i := 0; ; {
		j := strings.Index(s[i:], word)
		if j < 0 {
			return false
		}
		j += i
		before, after := rune(0), rune(0)
		if j > 0 {
			r := []rune(s[:j])
			before = r[len(r)-1]
		}
		if end := j + len(word); end < len(s) {
			for _, r := range s[end:] {
				after = r
				break
			}
		}
		if !ascii(before) && !ascii(after) {
			return true
		}
		i = j + 1
	}
}
