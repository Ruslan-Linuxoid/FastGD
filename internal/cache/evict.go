package cache

import (
	"cmp"
	"errors"
	"io/fs"
	"os"
	"slices"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

const evictEvery = 10 * time.Second

func (c *Cache) keep() {
	defer c.wg.Done()
	t := time.NewTicker(keepEvery)
	defer t.Stop()
	free := time.NewTicker(freeEvery)
	defer free.Stop()
	reclaim := time.NewTimer(reclaimAfter)
	defer reclaim.Stop()
	for {
		select {
		case <-c.bg.Done():
			return
		case <-t.C:
			c.refreshLimit(true)
			c.trim()
		case <-c.trims:
			c.trim()
		case <-free.C:
			c.watchFree()
		case <-reclaim.C:
			if err := c.Reclaim(); err != nil {
				c.log.Warn("go over the cache's directory", "err", err)
			}
		}
	}
}

func (c *Cache) trim() {
	c.evicting.Lock()
	defer c.evicting.Unlock()
	if early := evictEvery - time.Since(c.evicted); early > 0 {
		if !c.waiting {
			c.waiting = true
			time.AfterFunc(early, c.trimSoon)
		}
		return
	}
	c.waiting = false
	over := c.over()
	if over <= 0 {
		return
	}
	c.evicted = time.Now()
	if err := c.evict(over); err != nil && !errors.Is(err, errClosed) {
		c.log.Warn("evict from the cache", "err", err)
	}
}

func (c *Cache) evict(need int64) error {
	freed, err := c.evictStrays(need)
	if err != nil {
		return err
	}
	need -= freed
	pieces, err := c.opt.Records.CachedPieces()
	if err != nil {
		return err
	}
	c.worths.mu.Lock()
	values := make([]float64, len(pieces))
	order := make([]int, len(pieces))
	for i, p := range pieces {
		values[i], order[i] = c.worths.valueOf(p), i
	}
	c.worths.mu.Unlock()
	slices.SortFunc(order, func(a, b int) int {
		return cmp.Or(cmp.Compare(values[a], values[b]), cmp.Compare(pieces[a].Ino, pieces[b].Ino), cmp.Compare(pieces[a].Off,
			pieces[b].Off))
	})
	for _, i := range order {
		if need <= 0 {
			break
		}
		n, err := c.evictPiece(pieces[i], values[i])
		if err != nil {
			return err
		}
		need -= n
	}
	return nil
}

func (c *Cache) evictPiece(p meta.CachedPiece, value float64) (int64, error) {
	g, err := c.current(p.Ino)
	if err != nil {
		return 0, err
	}
	lock := c.fileLock(p.Ino)
	lock.Lock()
	defer lock.Unlock()
	s := pieceSpan(p.Off, p.Len)
	if !c.takeable(g, p, s) {
		return 0, nil
	}
	g.recMu.Lock()
	defer g.recMu.Unlock()
	if err := c.opt.Records.DeletePiece(p.Ino, p.Off); err != nil {
		return 0, err
	}
	n, last := c.forgetPiece(g, s)
	if last {
		err = os.Remove(c.path(p.ID))
	} else {
		err = c.punch(c.path(p.ID), s)
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		c.log.Warn("cut an evicted piece out of its file", "id", p.ID, "off", s.off, "err", err)
	}
	c.release(n)
	c.worths.evicted(pieceAt{p.Ino, p.Off}, value)
	return n, nil
}

func (c *Cache) takeable(g *gen, p meta.CachedPiece, s span) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gens[p.Ino] == g && !g.local && !g.stale && g.refs == 0 && g.pieces.index(s) >= 0
}

func (c *Cache) forgetPiece(g *gen, s span) (int64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	g.pieces.remove(s)
	n := PageUp(s.len())

	last := len(g.pieces) == 0 && len(g.inflight) == 0
	if len(g.pieces) == 0 && g.extentPageReserved {
		g.extentPageReserved = false
		n += page()
	}
	if last {
		g.synced = false
	}
	return n, last
}
