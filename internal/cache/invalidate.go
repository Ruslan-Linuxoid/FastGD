package cache

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func (c *Cache) Invalidate(ino meta.Ino, id string) error { return c.replace(ino, id, c.newGen(nil)) }

func (c *Cache) Remove(ino meta.Ino, id string) error { return c.replace(ino, id, nil) }

func (c *Cache) replace(ino meta.Ino, id string, next *gen) error {

	if _, err := c.current(ino); err != nil {
		return err
	}
	lock := c.fileLock(ino)
	lock.Lock()
	defer lock.Unlock()

	oldFD, _ := os.Open(c.path(id))
	freed, oldFD := c.retire(ino, next, oldFD)
	if oldFD != nil {
		oldFD.Close()
	}
	if err := c.opt.Records.DeletePieces(ino); err != nil {
		return err
	}
	if err := removeName(c.path(id)); err != nil {
		return err
	}
	c.release(freed)
	return nil
}

func (c *Cache) retire(ino meta.Ino, next *gen, oldFD *os.File) (int64, *os.File) {
	c.mu.Lock()
	defer c.mu.Unlock()
	old := c.gens[ino]
	delete(c.gens, ino)
	c.worths.forgetVersion(ino)
	if next != nil {
		c.gens[ino] = next
	}
	var freed int64
	if old != nil && !old.local {
		freed = old.roomOf()
	}
	if old != nil {
		old.stale = true
		c.stopDownloads(old)
	}
	if old != nil && old.refs > 0 && len(old.pieces) > 0 && oldFD != nil {
		old.fd, oldFD = oldFD, nil
	}
	return freed, oldFD
}

func (c *Cache) stopDownloads(old *gen) {
	for _, fl := range old.inflight {
		fl.cancel()
		if got := fl.arrivedSpan(); got.len() > 0 {
			old.pieces.add(got)
		}
	}
	old.inflight = nil
	c.noteFetching(old)
}

func (c *Cache) forget(g *gen, ino meta.Ino) error {
	lock := c.fileLock(ino)
	lock.Lock()
	defer lock.Unlock()
	if !c.isCurrent(g, ino) {
		return nil
	}
	return c.dropRecords(g, ino)
}

func (c *Cache) dropRecords(g *gen, ino meta.Ino) error {
	g.recMu.Lock()
	defer g.recMu.Unlock()
	if err := c.opt.Records.DeletePieces(ino); err != nil {
		return err
	}
	c.mu.Lock()
	var freed int64
	if !g.local {
		freed = g.roomOf()
	}
	g.pieces, g.extentPageReserved = nil, false
	c.mu.Unlock()
	c.worths.forget(ino)
	c.release(freed)
	return nil
}

func removeName(p string) error {
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("cache: %w", err)
	}
	return nil
}
