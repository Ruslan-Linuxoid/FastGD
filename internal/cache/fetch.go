package cache

import (
	"context"
	"errors"
	"time"
)

const (
	TaskRetryFirst = 2 * time.Second
	TaskRetryMax   = 10 * time.Minute
)

func TaskRetrySteps() int {
	n := 0
	for wait := TaskRetryFirst; wait < TaskRetryMax; wait *= 2 {
		n++
	}
	return n
}

func (c *Cache) claim(g *gen, f File, want span) ([]*fetch, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil, false, errClosed
	}
	if err := g.refuses(f); err != nil {
		return nil, false, err
	}
	var waits []*fetch
	for _, fl := range g.inflight {
		if fl.off < want.end && fl.end > want.off {
			waits = append(waits, fl)
		}
	}
	gaps := g.missing(want)
	for _, gap := range gaps {
		waits = append(waits, c.start(g, f, gap, false))
	}
	for _, fl := range waits {
		fl.hold()
	}
	return waits, len(gaps) > 0, nil
}

func (c *Cache) start(g *gen, f File, s span, prefetch bool) *fetch {

	ctx, cancel := context.WithCancel(c.bg)
	fl := &fetch{
		span: s, prefetch: prefetch, cancel: cancel,
		wanted: make(chan struct{}), tick: make(chan struct{}), refs: 1,
	}
	g.inflight = append(g.inflight, fl)
	c.noteFetching(g)
	c.wg.Go(func() {
		c.download(ctx, g, f, fl)
	})
	return fl
}

func (c *Cache) prefetch(g *gen, f File, s span) {
	if s.len() <= 0 || s.off >= f.Base {
		return
	}
	want := pageSpan(s.off, s.end, f.Base)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed || g.stale || g.local {
		return
	}
	for _, gap := range g.missing(want) {
		c.start(g, f, gap, true)
	}
}

func (c *Cache) download(ctx context.Context, g *gen, f File, fl *fetch) {
	defer fl.cancel()
	err := c.fill(ctx, g, f, fl)
	if err == nil {
		err = c.record(g, f, fl)
		if err != nil && !errors.Is(err, errSwitched) {
			c.log.Warn("keep a downloaded piece", "id", f.ID, "off", fl.off, "err", err)
		}
	}
	if err != nil {
		c.mu.Lock()
		g.drop(fl)
		c.noteFetching(g)
		c.mu.Unlock()
		fl.mu.Lock()
		fl.last = func() { c.dropFetch(g, f, fl) }
		fl.mu.Unlock()
	}
	fl.finish(err)
	fl.release()
}

func (c *Cache) dropFetch(g *gen, f File, fl *fetch) {
	if fl.reserved.Load() == 0 {
		return
	}
	lock := c.fileLock(f.Ino)
	lock.Lock()
	c.mu.Lock()
	var cut []span
	if c.gens[f.Ino] == g && !g.stale && !g.local {

		arrived := fl.arrivedSpan()
		arrived.end = PageUp(arrived.end)
		for _, gap := range g.pieces.gaps(arrived) {
			cut = append(cut, g.missing(gap)...)
		}
	}
	c.mu.Unlock()
	for _, s := range cut {
		if err := c.punch(c.path(f.ID), s); err != nil {
			c.log.Warn("cut a failed download out of its file", "id", f.ID, "off", s.off, "err", err)
		}
	}
	lock.Unlock()
	c.release(fl.reserved.Load())
}
