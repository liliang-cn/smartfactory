package series

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"errors"
	"io"
	"math"
	"math/bits"
	"sort"
)

// Point is one numeric reading.
type Point struct {
	T   int64 // unix milliseconds
	V   float64
	Q   uint8  // quality flags (tags.Flag*)
	Src uint32 // source id
}

// A chunk is one series' readings in one hour: time deltas as varints, each value XORed with
// the one before (repeated and slowly moving values cost a byte or two), flags only where set,
// then deflated. Readings every second come to a few bytes each.
func encodePoints(base int64, ps []Point) []byte {
	var b bytes.Buffer
	var tmp [binary.MaxVarintLen64]byte
	put := func(x uint64) { b.Write(tmp[:binary.PutUvarint(tmp[:], x)]) }
	put(uint64(len(ps)))
	prevT, prevV := base, uint64(0)
	for _, p := range ps {
		put(uint64(p.T - prevT))
		prevT = p.T
		x := math.Float64bits(p.V) ^ prevV
		prevV = math.Float64bits(p.V)
		if x == 0 {
			b.WriteByte(0xff)
			continue
		}
		tz := bits.TrailingZeros64(x)
		b.WriteByte(byte(tz))
		put(x >> tz)
	}
	src := uint32(0)
	for i, p := range ps {
		if i == 0 || p.Src != src {
			put(uint64(i))
			put(uint64(p.Src))
			src = p.Src
		}
	}
	put(uint64(len(ps))) // end of source runs
	for i, p := range ps {
		if p.Q != 0 {
			put(uint64(i))
			b.WriteByte(p.Q)
		}
	}
	var out bytes.Buffer
	w, _ := flate.NewWriter(&out, flate.BestSpeed)
	w.Write(b.Bytes())
	w.Close()
	return out.Bytes()
}

var errChunk = errors.New("series: corrupt chunk")

func decodePoints(base int64, data []byte) ([]Point, error) {
	raw, err := io.ReadAll(flate.NewReader(bytes.NewReader(data)))
	if err != nil {
		return nil, err
	}
	r := bytes.NewReader(raw)
	n, err := binary.ReadUvarint(r)
	if err != nil || n > 1<<24 {
		return nil, errChunk
	}
	ps := make([]Point, n)
	prevT, prevV := base, uint64(0)
	for i := range ps {
		dt, err := binary.ReadUvarint(r)
		if err != nil {
			return nil, errChunk
		}
		prevT += int64(dt)
		ps[i].T = prevT
		tz, err := r.ReadByte()
		if err != nil {
			return nil, errChunk
		}
		if tz != 0xff {
			x, err := binary.ReadUvarint(r)
			if err != nil || tz > 63 {
				return nil, errChunk
			}
			prevV ^= x << tz
		}
		ps[i].V = math.Float64frombits(prevV)
	}
	for {
		i, err := binary.ReadUvarint(r)
		if err != nil {
			return nil, errChunk
		}
		if i >= n {
			break
		}
		src, err := binary.ReadUvarint(r)
		if err != nil {
			return nil, errChunk
		}
		for j := i; j < n; j++ {
			ps[j].Src = uint32(src)
		}
	}
	for {
		i, err := binary.ReadUvarint(r)
		if err == io.EOF {
			break
		}
		if err != nil || i >= n {
			return nil, errChunk
		}
		q, err := r.ReadByte()
		if err != nil {
			return nil, errChunk
		}
		ps[i].Q = q
	}
	return ps, nil
}

// mergePoints puts b into a in time order; a reading at the same instant as one already there
// replaces it (a gateway sending the same reading twice).
func mergePoints(a, b []Point) []Point {
	all := append(append(make([]Point, 0, len(a)+len(b)), a...), b...)
	sort.SliceStable(all, func(i, j int) bool { return all[i].T < all[j].T })
	out := all[:0]
	for _, p := range all {
		if len(out) > 0 && out[len(out)-1].T == p.T {
			out[len(out)-1] = p
			continue
		}
		out = append(out, p)
	}
	return out
}

// Agg is the readings of one interval. Doubtful readings are counted in Flagged and left out
// of the rest; N is 0 when every reading was doubtful.
type Agg struct {
	T                   int64 // start of the interval, unix milliseconds
	N                   int
	Min, Max, Sum, Last float64
	Flagged             int
}

func (a Agg) Avg() float64 { return a.Sum / float64(a.N) }

// aggregate groups points into intervals of width ms starting at base.
func aggregate(base, width int64, ps []Point, doubtful uint8) []Agg {
	var out []Agg
	for _, p := range ps {
		t := base + (p.T-base)/width*width
		if len(out) == 0 || out[len(out)-1].T != t {
			out = append(out, Agg{T: t, Min: math.Inf(1), Max: math.Inf(-1)})
		}
		a := &out[len(out)-1]
		if p.Q&doubtful != 0 {
			a.Flagged++
			continue
		}
		a.N++
		a.Sum += p.V
		a.Min = math.Min(a.Min, p.V)
		a.Max = math.Max(a.Max, p.V)
		a.Last = p.V
	}
	return out
}

// combine folds finer aggregates into one (an hour from its minutes).
func combine(t int64, as []Agg) Agg {
	c := Agg{T: t, Min: math.Inf(1), Max: math.Inf(-1)}
	for _, a := range as {
		c.Flagged += a.Flagged
		if a.N == 0 {
			continue
		}
		c.N += a.N
		c.Sum += a.Sum
		c.Min = math.Min(c.Min, a.Min)
		c.Max = math.Max(c.Max, a.Max)
		c.Last = a.Last
	}
	return c
}

// An aggregate block holds the intervals of one hour (minutes) or one day (hours): the slot
// of each, then its numbers, deflated.
func encodeAggs(base, width int64, as []Agg) []byte {
	var b bytes.Buffer
	var tmp [binary.MaxVarintLen64]byte
	put := func(x uint64) { b.Write(tmp[:binary.PutUvarint(tmp[:], x)]) }
	f := func(x float64) { binary.Write(&b, binary.LittleEndian, x) }
	put(uint64(len(as)))
	for _, a := range as {
		put(uint64((a.T - base) / width))
		put(uint64(a.N))
		put(uint64(a.Flagged))
		if a.N > 0 {
			f(a.Min)
			f(a.Max)
			f(a.Sum)
			f(a.Last)
		}
	}
	var out bytes.Buffer
	w, _ := flate.NewWriter(&out, flate.BestSpeed)
	w.Write(b.Bytes())
	w.Close()
	return out.Bytes()
}

func decodeAggs(base, width int64, data []byte) ([]Agg, error) {
	raw, err := io.ReadAll(flate.NewReader(bytes.NewReader(data)))
	if err != nil {
		return nil, err
	}
	r := bytes.NewReader(raw)
	n, err := binary.ReadUvarint(r)
	if err != nil || n > 1<<16 {
		return nil, errChunk
	}
	out := make([]Agg, n)
	for i := range out {
		slot, e1 := binary.ReadUvarint(r)
		cnt, e2 := binary.ReadUvarint(r)
		fl, e3 := binary.ReadUvarint(r)
		if e1 != nil || e2 != nil || e3 != nil {
			return nil, errChunk
		}
		a := Agg{T: base + int64(slot)*width, N: int(cnt), Flagged: int(fl), Min: math.NaN(), Max: math.NaN()}
		if cnt > 0 {
			var v [4]float64
			if err := binary.Read(r, binary.LittleEndian, &v); err != nil {
				return nil, errChunk
			}
			a.Min, a.Max, a.Sum, a.Last = v[0], v[1], v[2], v[3]
		}
		out[i] = a
	}
	return out, nil
}

// putAgg replaces the interval at a.T (or adds it), keeping time order.
func putAgg(as []Agg, a Agg) []Agg {
	i := sort.Search(len(as), func(i int) bool { return as[i].T >= a.T })
	if i < len(as) && as[i].T == a.T {
		as[i] = a
		return as
	}
	as = append(as, Agg{})
	copy(as[i+1:], as[i:])
	as[i] = a
	return as
}

// indexAgg is the position of the interval starting at t, or -1.
func indexAgg(as []Agg, t int64) int {
	i := sort.Search(len(as), func(i int) bool { return as[i].T >= t })
	if i < len(as) && as[i].T == t {
		return i
	}
	return -1
}
