package cache

import (
	"slices"
	"sync"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

const evictedKept = 4096

type pieceAt struct {
	ino meta.Ino
	off int64
}

type worth struct {
	hits int64

	floor float64

	fresh bool
}

type worths struct {
	mu sync.Mutex

	of    map[meta.Ino]map[int64]*worth
	floor float64

	gone  map[pieceAt]int64
	order []pieceAt

	local map[meta.Ino]int64
}

func (w *worths) valueOf(p meta.CachedPiece) float64 {
	hits, floor := int64(1), 0.0
	if known := w.of[p.Ino][p.Off]; known != nil {
		hits, floor = known.hits, known.floor
	}
	return floor + float64(hits)/float64(max(PageUp(p.Len)/page(), 1))
}

func (w *worths) evicted(at pieceAt, value float64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.floor = max(w.floor, value)
	hits := int64(1)
	if known := w.of[at.ino][at.off]; known != nil {
		hits = known.hits
	}
	delete(w.of[at.ino], at.off)
	if len(w.of[at.ino]) == 0 {
		delete(w.of, at.ino)
	}
	if _, ok := w.gone[at]; !ok {
		w.order = append(w.order, at)
	}
	w.gone[at] = hits
	for len(w.order) > evictedKept {
		delete(w.gone, w.order[0])
		w.order = w.order[1:]
	}
}

func (w *worths) known(ino meta.Ino, off, hits int64) *worth {
	if w.of[ino] == nil {
		w.of[ino] = map[int64]*worth{}
	}
	known := w.of[ino][off]
	if known == nil {
		known = &worth{hits: hits}
		w.of[ino][off] = known
	}
	return known
}

func (w *worths) hit(ino meta.Ino, off int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	known := w.known(ino, off, 1)
	if known.fresh {
		known.fresh = false
		return
	}
	known.hits++
	known.floor = w.floor
}

func (w *worths) came(ino meta.Ino, s span, reads int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	known := w.known(ino, s.off, 0)
	known.hits += max(reads, 1)
	known.floor, known.fresh = w.floor, reads == 0
	w.order = slices.DeleteFunc(w.order, func(at pieceAt) bool {
		if at.ino != ino || at.off < s.off || at.off >= s.end {
			return false
		}
		known.hits += w.gone[at]
		delete(w.gone, at)
		return true
	})
}

func (w *worths) localHit(ino meta.Ino) {
	w.mu.Lock()
	w.local[ino]++
	w.mu.Unlock()
}

func (w *worths) adopted(ino meta.Ino, size int64) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.of, ino)
	if size > 0 {
		w.of[ino] = map[int64]*worth{0: {hits: max(w.local[ino], 1), floor: w.floor}}
	}
	delete(w.local, ino)
}

func (w *worths) joined(ino meta.Ino, pieces spans) {
	w.mu.Lock()
	defer w.mu.Unlock()
	was := w.of[ino]
	now := map[int64]*worth{}
	for off, known := range was {
		s, ok := pieces.at(off)
		if !ok {
			continue
		}
		if into := now[s.off]; into != nil {
			into.hits += known.hits
			into.floor = max(into.floor, known.floor)
		} else {
			now[s.off] = &worth{hits: known.hits, floor: known.floor}
		}
	}
	w.of[ino] = now
}

func (w *worths) forget(ino meta.Ino) {
	w.mu.Lock()
	delete(w.of, ino)
	w.mu.Unlock()
}

func (w *worths) forgetVersion(ino meta.Ino) {
	w.mu.Lock()
	defer w.mu.Unlock()
	delete(w.of, ino)
	delete(w.local, ino)
	w.order = slices.DeleteFunc(w.order, func(at pieceAt) bool {
		if at.ino != ino {
			return false
		}
		delete(w.gone, at)
		return true
	})
}
