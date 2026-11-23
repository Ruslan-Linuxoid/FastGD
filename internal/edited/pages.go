package edited

import (
	"fmt"
	"os"

	"github.com/Ruslan-Linuxoid/FastGD/internal/cache"
)

func page() int64 { return cache.HoleUnit() }

type span struct {
	off, end int64
}

func (s span) len() int64 { return s.end - s.off }

func pageSpan(off, end, size int64) span {
	return span{off: cache.PageDown(off), end: min(size, cache.PageUp(end))}
}

func keptBase(off, end, base int64) []span {
	from := min(cache.PageUp(off), base)
	to := base
	if end < base {
		to = cache.PageDown(end)
	}
	if base <= 0 {
		return nil
	}
	if end <= off || to <= from {
		return []span{{0, base}}
	}
	var out []span
	for _, k := range []span{{0, from}, {to, base}} {
		if k.end > k.off {
			out = append(out, k)
		}
	}
	return out
}

func holeFlush() int64 { return cache.Portion() }

func tailOf(base int64) span {
	if base%page() == 0 {
		return span{}
	}
	return span{cache.PageDown(base), base}
}

func tailMissing(fd *os.File, base int64) (bool, error) {
	t := tailOf(base)
	if t.len() == 0 {
		return false, nil
	}
	holes, err := holesIn(fd, span{t.off, t.off + 1})
	return len(holes) > 0, err
}

func edges(off, end, base int64) []span {
	if end <= off {
		return nil
	}
	var out []span
	for _, pg := range []int64{cache.PageDown(off), cache.PageDown(end - 1)} {
		b := span{pg, min(pg+page(), base)}
		touched := off < b.end && end > b.off
		if touched && (off > b.off || end < b.end) && (len(out) == 0 || out[0] != b) {
			out = append(out, b)
		}
	}
	return out
}

func (w *working) baseNow() int64 {
	w.pages.Lock()
	defer w.pages.Unlock()
	return w.base
}

func (w *working) missing(fd *os.File, id string, want func(base int64) span) ([]span, error) {
	w.pages.Lock()
	holes, err := w.missingIn(fd, want(w.base))
	w.pages.Unlock()
	if err != nil {
		return nil, fmt.Errorf("edited: %s: %w", id, err)
	}
	return holes, nil
}

func wholeBase(base int64) span { return span{0, base} }

func (w *working) missingIn(fd *os.File, s span) ([]span, error) {
	s.end = min(s.end, w.base)
	if s.len() <= 0 {
		return nil, nil
	}
	holes, err := holesIn(fd, s)
	if err != nil || !w.tail {
		return holes, err
	}
	t := tailOf(w.base)
	t.off, t.end = max(t.off, s.off), min(t.end, s.end)
	if t.len() <= 0 {
		return holes, nil
	}

	switch n := len(holes); {
	case n > 0 && holes[n-1].end > t.off:
		return holes, nil
	case n > 0 && holes[n-1].end == t.off:
		holes[n-1].end = t.end
		return holes, nil
	}
	return append(holes, t), nil
}
