package shapes

import (
	"fmt"
	"math"
	"sort"

	"github.com/liliang-cn/agent-go/v3/pkg/codemode"
)

// errFn reports an error of a category at a program line, against a node.
type errFn func(cat string, line int, node, format string, a ...any)

// realSize refuses a product whose longest side is far from what such a thing really measures.
func realSize(spec Spec, size [3]float64, errorf errFn, c *codemode.Check) {
	if spec.RealFrom == "" {
		return
	}
	real, drawn := longest(spec.Real), longest(size)
	f := realGivenFactor
	if spec.RealFrom == RealEstimated {
		f = realEstimatedFactor
	}
	name := spec.Name
	if name == "" {
		name = "unit"
	}
	src := "the product data says"
	if spec.RealFrom == RealEstimated {
		src = "its weight suggests"
	}
	c.Stats = append(c.Stats, codemode.Stat{Key: "real size", Value: fmt.Sprintf("%s about %s m on the longest side; accepted %s to %s m; drawn %s m",
		src, fnum(real), fnum(real/f), fnum(real*f), fnum(drawn))})
	if drawn > real*f || drawn < real/f {
		errorf("size", 0, "", "the longest side of the %s is %s m, but %s about %s m (accepted %s to %s m). Scale the whole drawing by about %s and keep its proportions",
			name, fnum(drawn), src, fnum(real), fnum(real/f), fnum(real*f), fnum(math.Round(real/drawn*100)/100))
	}
}

// gap is the distance between two boxes (0 when they touch or overlap): the largest separation
// along any axis.
func gap(a, b Placed) float64 {
	g := 0.0
	for k := range 3 {
		g = math.Max(g, math.Max(a.Min[k]-b.Max[k], b.Min[k]-a.Max[k]))
	}
	return g
}

// joined refuses parts that float apart from the rest: the bounding boxes have to touch or
// overlap (within a small tolerance) in a chain from every part to the others. A machine's parts
// resting on the floor are joined to it; parts already there (add-on mode) are what new ones may
// attach to. A machine's built-in body is not known here, so with one the largest group that
// touches nothing known is let through (it is taken to sit on the body).
func joined(spec Spec, all []Placed, errorf errFn, c *codemode.Check) {
	n := len(all)
	if n < 2 && !(n == 1 && spec.Target == Machine) {
		return
	}
	tol := joinMachine
	if spec.Target == Product {
		tol = joinProduct
	}
	ground := n // the floor, a node of its own
	parent := make([]int, n+1)
	for i := range parent {
		parent[i] = i
	}
	var find func(int) int
	find = func(i int) int {
		for parent[i] != i {
			parent[i] = parent[parent[i]]
			i = parent[i]
		}
		return i
	}
	union := func(a, b int) { parent[find(a)] = find(b) }
	for i := range all {
		if spec.Target == Machine && all[i].Min[1] <= tol {
			union(i, ground)
		}
		for j := i + 1; j < n; j++ {
			if gap(all[i], all[j]) <= tol {
				union(i, j)
			}
		}
	}
	type group struct {
		parts    []int
		anchored bool
		volume   float64
	}
	groups := map[int]*group{}
	for i, p := range all {
		g := groups[find(i)]
		if g == nil {
			g = &group{}
			groups[find(i)] = g
		}
		g.parts = append(g.parts, i)
		g.volume += p.volume()
		if p.Old {
			g.anchored = true
		}
	}
	anyAnchor := false
	if g := groups[find(ground)]; g != nil && spec.Target == Machine {
		g.anchored = true
	}
	for _, g := range groups {
		anyAnchor = anyAnchor || g.anchored
	}
	c.Stats = append(c.Stats, codemode.Stat{Key: "joined groups", Value: fmt.Sprintf("%d (1 is right: every part touches the rest within %s m)", countGroups(groups), fnum(tol))})

	// the groups with a new part that hold on to nothing known
	var loose []*group
	for _, g := range groups {
		if g.anchored {
			continue
		}
		for _, i := range g.parts {
			if !all[i].Old && i != ground {
				loose = append(loose, g)
				break
			}
		}
	}
	unknownBody := spec.Append && spec.Target == Machine && spec.Body != "" && spec.Body != "frame"
	sort.Slice(loose, func(a, b int) bool { return loose[a].volume > loose[b].volume })
	if len(loose) > 0 && (!anyAnchor || unknownBody) {
		loose = loose[1:] // the biggest is taken to be the main body (or to sit on the built-in one)
	}
	sort.Slice(loose, func(a, b int) bool { return firstNew(all, loose[a].parts) < firstNew(all, loose[b].parts) })
	for k, g := range loose {
		if k == 5 {
			errorf("gap", 0, "", "%d groups of parts in all float apart from the rest", len(loose))
			break
		}
		in := map[int]bool{}
		for _, i := range g.parts {
			in[i] = true
		}
		best, with, from := math.Inf(1), "", -1
		for _, i := range g.parts {
			for j := range all {
				if in[j] {
					continue
				}
				if d := gap(all[i], all[j]); d < best {
					best, with, from = d, all[j].Path, i
				}
			}
			if spec.Target == Machine && all[i].Min[1] < best {
				best, with, from = all[i].Min[1], "the floor", i
			}
		}
		if from < 0 {
			from = firstNew(all, g.parts)
		}
		what := "the other parts"
		if with != "" {
			what = fmt.Sprintf("%q", with)
			if with == "the floor" {
				what = with
			}
		}
		hint := "move it so it touches or overlaps a few millimetres, or add a part that bridges the gap"
		errorf("gap", all[from].Line, all[from].Path, "floats apart: %s m from %s (parts must touch within %s m); %s", fnum(best), what, fnum(tol), hint)
	}
}

func countGroups[T any](m map[int]T) int { return len(m) }

// firstNew is the first new part of a group by program order (or its first part).
func firstNew(all []Placed, parts []int) int {
	best := parts[0]
	for _, i := range parts {
		if i < len(all) && !all[i].Old && (all[best].Old || i < best) {
			best = i
		}
	}
	return best
}
