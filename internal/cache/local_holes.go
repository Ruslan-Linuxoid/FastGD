package cache

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

var ErrLocal = errors.New("cache: the file is changed here")

const HeldPrefix = "held-"

func (c *Cache) HandOver(ino meta.Ino, id string, base int64) (err error) {
	g, err := c.current(ino)
	if err != nil {
		return err
	}
	stopping := c.stopForHandOver(g)
	defer func() {
		if err != nil {

			c.mu.Lock()
			g.local = false
			c.mu.Unlock()
		}
	}()

	for _, fl := range stopping {
		fl.waitEnd()
	}
	lock := c.fileLock(ino)
	lock.Lock()
	defer lock.Unlock()
	fd, _, err := c.openForWrite(c.path(id))
	if err != nil {
		return fmt.Errorf("cache: %w", err)
	}
	defer fd.Close()
	if err := c.punchUnrecorded(fd, g, id, base); err != nil {
		return err
	}
	if err := fd.Truncate(base); err != nil {
		return fmt.Errorf("cache: %w", err)
	}
	c.dirtyMoved.Store(true)
	if err := fd.Sync(); err != nil {
		return fmt.Errorf("cache: sync %s: %w", id, err)
	}

	c.mu.Lock()
	recorded := g.roomOf()
	g.extentPageReserved = false
	c.mu.Unlock()
	c.release(recorded)

	c.worths.forget(ino)
	return nil
}

func (c *Cache) stopForHandOver(g *gen) []*fetch {
	c.mu.Lock()
	defer c.mu.Unlock()
	g.local = true
	stopping := g.inflight
	g.inflight = nil
	c.noteFetching(g)
	for _, fl := range stopping {
		fl.cancel()
	}
	return stopping
}

func (c *Cache) punchUnrecorded(fd *os.File, g *gen, id string, base int64) error {
	c.mu.Lock()
	unrecorded := g.pieces.gaps(span{0, base})
	c.mu.Unlock()
	for _, gap := range unrecorded {

		if err := c.opt.punchHoleAt(int(fd.Fd()), gap.off, PageUp(gap.end)-gap.off); err != nil {
			return fmt.Errorf("cache: punch %s at %d: %w", id, gap.off, err)
		}
	}
	return nil
}

func (c *Cache) MarkEdited(ino meta.Ino) error {
	g, err := c.current(ino)
	if err != nil {
		return err
	}
	c.mu.Lock()

	g.local, g.extentPageReserved = true, false
	c.mu.Unlock()
	return nil
}

func (c *Cache) Path(id string) string { return c.path(id) }

func (c *Cache) Dir() string { return c.opt.Dir }

func (c *Cache) LowDisk() bool { return c.lowDisk.Load() }

func (c *Cache) Touched(ino meta.Ino) { c.worths.localHit(ino) }

func (c *Cache) Changed() { c.dirtyMoved.Store(true) }

func (c *Cache) Fetch(ctx context.Context, f File, ranges [][2]int64) error {
	g, err := c.current(f.Ino)
	if err != nil {
		return err
	}
	for _, r := range ranges {

		waits, _, err := c.claim(g, f, span{r[0], r[1]})
		if errors.Is(err, errSwitched) {
			return ErrStale
		}
		if err != nil {
			return err
		}
		for _, fl := range waits {
			if err == nil {
				err = fl.wait(ctx, fl.end)
			}
			if err == nil {
				err = fl.waitEnd()
			}
			fl.release()
		}
		if err != nil {

			return c.stopped(g, err, ErrStale)
		}
	}
	return nil
}
