package cache

import (
	"cmp"
	"slices"
	"sync/atomic"
)

const defaultUnit = 4 << 10

var unit atomic.Int64

func page() int64 {
	if u := unit.Load(); u != 0 {
		return u
	}
	return defaultUnit
}

func HoleUnit() int64 { return page() }

const leastPortion = 256 << 10

func Portion() int64 { return portionOf(page()) }

func portionOf(unit int64) int64 { return (leastPortion + unit - 1) / unit * unit }

type span struct {
	off, end int64
}

func (s span) len() int64 { return s.end - s.off }

func (s span) holds(at int64) bool { return at >= s.off && at <= s.end }

func pageSpan(off, end, size int64) span {
	return span{off: PageDown(off), end: min(size, PageUp(end))}
}

type spans []span

func (ss spans) search(pos int64) (int, bool) {
	return slices.BinarySearchFunc(ss, pos, func(s span, pos int64) int {
		if s.end <= pos {
			return -1
		}
		if s.off > pos {
			return 1
		}
		return 0
	})
}

func (ss spans) at(pos int64) (span, bool) {
	if i, ok := ss.search(pos); ok {
		return ss[i], true
	}
	return span{}, false
}

func (ss spans) gaps(want span) []span {
	var out []span
	pos := want.off
	i, _ := ss.search(want.off)
	for ; i < len(ss) && ss[i].off < want.end && pos < want.end; i++ {
		if ss[i].off > pos {
			out = append(out, span{pos, min(ss[i].off, want.end)})
		}
		pos = max(pos, ss[i].end)
	}
	if pos < want.end {
		out = append(out, span{pos, want.end})
	}
	return out
}

func (ss spans) covers(want span) bool { return want.len() <= 0 || len(ss.gaps(want)) == 0 }

func (ss *spans) add(s span) {
	i, _ := slices.BinarySearchFunc(*ss, s.off, func(x span, off int64) int { return cmp.Compare(x.off, off) })
	*ss = slices.Insert(*ss, i, s)
}

func (ss spans) index(s span) int {
	if i, _ := ss.search(s.off); i < len(ss) && ss[i] == s {
		return i
	}
	return -1
}

func (ss *spans) remove(s span) {
	if i := ss.index(s); i >= 0 {
		*ss = slices.Delete(*ss, i, i+1)
	}
}

func (ss spans) joined() spans {
	var out spans
	for _, s := range ss {
		if n := len(out); n > 0 && out[n-1].end == s.off {
			out[n-1].end = s.end
			continue
		}
		out = append(out, s)
	}
	return out
}

func PageUp(n int64) int64 { return (n + page() - 1) / page() * page() }

func PageDown(n int64) int64 { return n / page() * page() }

func (ss spans) bytesOn() int64 {
	var n int64
	for _, s := range ss {
		n += PageUp(s.len())
	}
	return n
}

func pieceSpan(off, n int64) span { return span{off, off + n} }
