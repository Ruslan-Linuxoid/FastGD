package cache

import (
	"sync"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func windowCap(amount, limit int64, share float64) int64 {
	return min(amount, int64(float64(limit)*share))
}

func sharedCap(limit int64, files int) int64 {
	return max(PageDown(limit/int64(files)), page())
}

type window struct {
	mu sync.Mutex

	prev span
	has  bool

	dir int

	n, gained int64

	far int64

	ahead int64
	size  int64
}

func (w *window) plan(off, end int64, hit bool, limit int64) span {
	w.mu.Lock()
	defer w.mu.Unlock()
	if end <= off || w.reordered(off, end) {
		return span{}
	}
	w.step(off, end)
	if w.n == 0 {
		return span{}
	}
	w.size = min(max(PageUp(grown(w.gained/w.n, w.n, limit)), page()), limit)
	if !hit {

		w.ahead = w.far
	}
	if w.dir > 0 {
		return w.forward()
	}
	return w.backward()
}

func grown(step, n, limit int64) int64 {
	if step > limit/n/n {
		return limit
	}
	return step * n * n
}

func (w *window) reordered(off, end int64) bool {
	if w.n == 0 {
		return false
	}
	back := max(w.size, w.prev.len())
	if w.dir > 0 {
		return end <= w.far && off >= w.far-back
	}
	return off >= w.far && end <= w.far+back
}

func (w *window) step(off, end int64) {
	dir, gained := w.continuation(off, end)
	w.prev, w.has = span{off, end}, true
	if dir == 0 {
		w.n, w.gained, w.dir = 0, 0, 0
		return
	}
	if dir != w.dir || w.n == 0 {

		w.n, w.gained, w.dir = 0, 0, dir
		w.far = end
		if dir < 0 {
			w.far = off
		}
		w.ahead = w.far
	}
	w.n++
	w.gained += gained
	if dir > 0 {
		w.far = max(w.far, end)
	} else {
		w.far = min(w.far, off)
	}
}

func (w *window) continuation(off, end int64) (int, int64) {
	p := w.prev
	switch {
	case !w.has:
		return 0, 0
	case p.holds(off) && end > p.end:
		return 1, end - p.end
	case w.inPlan(off, end):

		return 1, end - max(off, w.far)
	case p.holds(end) && off < p.off:
		return -1, p.off - off
	}
	return 0, 0
}

func (w *window) inPlan(off, end int64) bool {
	return w.dir > 0 && w.n > 0 && off > w.prev.end && off < w.ahead && end > w.far
}

func (w *window) forward() span {
	w.ahead = max(w.ahead, w.far)
	if w.ahead-w.far >= w.size/2 {
		return span{}
	}
	s := span{w.ahead, w.ahead + w.size}
	w.ahead = s.end
	return s
}

func (w *window) backward() span {
	w.ahead = min(w.ahead, w.far)
	if w.far-w.ahead >= w.size/2 || w.ahead <= 0 {
		return span{}
	}
	s := span{max(w.ahead-w.size, 0), w.ahead}
	w.ahead = s.off
	return s
}

func (c *Cache) WindowCap() int64 {
	return windowCap(c.meter.Amount(c.opt.TransferTime), c.Limit(), c.opt.Share)
}

func (c *Cache) WindowCapOf(ino meta.Ino) int64 {
	c.mu.Lock()
	files := len(c.fetching)
	if _, own := c.fetching[c.gens[ino]]; !own {
		files++
	}
	c.mu.Unlock()
	return sharedCap(c.WindowCap(), files)
}
