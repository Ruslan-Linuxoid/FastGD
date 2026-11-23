package cache

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func (c *Cache) Holds(id string) bool {
	_, err := os.Lstat(c.path(id))
	return err == nil
}

func (c *Cache) PutLocal(id string, content []byte) error {
	fd, _, err := c.openForWrite(c.path(id))
	if err != nil {
		return fmt.Errorf("cache: %w", err)
	}
	defer fd.Close()
	if err := fd.Truncate(0); err != nil {
		return fmt.Errorf("cache: %w", err)
	}
	if _, err := fd.WriteAt(content, 0); err != nil {
		return fmt.Errorf("cache: %w", err)
	}
	if err := fd.Sync(); err != nil {
		return fmt.Errorf("cache: %w", err)
	}
	return nil
}

func (c *Cache) CreateLocal(id string) error {
	fd, _, err := c.openForWrite(c.path(id))
	if err != nil {
		return fmt.Errorf("cache: %w", err)
	}
	return fd.Close()
}

func (c *Cache) SyncLocal(id string) error {
	fd, err := os.OpenFile(c.path(id), os.O_WRONLY, 0)
	if err != nil {
		return fmt.Errorf("cache: %w", err)
	}
	err = unix.Fdatasync(int(fd.Fd()))
	if cerr := fd.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("cache: sync %s: %w", id, err)
	}
	return nil
}

func (c *Cache) OpenLocal(id string) (*os.File, error) {
	fd, err := os.Open(c.path(id))
	if err != nil {
		return nil, fmt.Errorf("cache: %w", err)
	}
	return fd, nil
}

func (c *Cache) LocalSize(id string) (int64, error) {
	fi, err := os.Stat(c.path(id))
	if err != nil {
		return 0, fmt.Errorf("cache: %w", err)
	}
	return fi.Size(), nil
}

func (c *Cache) Adopt(ino meta.Ino, size int64) error {
	g, err := c.current(ino)
	if err != nil {
		return err
	}
	g.recMu.Lock()
	defer g.recMu.Unlock()
	c.mu.Lock()
	g.local = false
	g.pieces = nil
	n := int64(0)
	if size > 0 {
		g.pieces = spans{{0, size}}
		n = PageUp(size)
		if !g.extentPageReserved {
			g.extentPageReserved = true
			n += page()
		}
	}
	c.mu.Unlock()
	c.worths.adopted(ino, size)
	c.take(n)

	c.refreshLimit(true)
	return nil
}

func (c *Cache) DropOldLayout() (kept int, err error) {
	items, err := c.opt.Records.Dirty()
	if err != nil {
		return 0, err
	}
	keep := map[string]bool{}
	for _, it := range items {
		keep[it.ID] = true
	}
	return len(keep), filepath.WalkDir(c.opt.Dir, func(p string, d fs.DirEntry, err error) error {
		return c.dropLeftover(keep, p, d, err)
	})
}

func (c *Cache) dropLeftover(keep map[string]bool, p string, d fs.DirEntry, err error) error {
	if err == nil && d.IsDir() && d.Name() == orphans {
		return fs.SkipDir
	}
	if err != nil || d.IsDir() || keep[d.Name()] || held(d.Name()) {
		return err
	}
	_, err = c.opt.Records.ByID(d.Name())
	if err != nil {
		if errors.Is(err, meta.ErrNotFound) {
			return nil
		}
		return err
	}
	return os.Remove(p)
}

func (c *Cache) LinkLocal(id, as string) error {
	if err := mkdirSynced(filepath.Dir(c.path(as))); err != nil {
		return fmt.Errorf("cache: %w", err)
	}

	c.takePath(c.path(as))
	if err := removeName(c.path(as)); err != nil {
		return err
	}
	c.mu.Lock()
	c.made[as] = true
	c.mu.Unlock()
	if err := os.Link(c.path(id), c.path(as)); err != nil {
		return fmt.Errorf("cache: %w", err)
	}
	return syncDir(filepath.Dir(c.path(as)))
}

func (c *Cache) UnlinkLocal(id string) error {
	c.mu.Lock()
	delete(c.made, id)
	c.mu.Unlock()
	if err := removeName(c.path(id)); err != nil {
		return err
	}
	return nil
}

func (c *Cache) UnlinkShared(id, as string) error {
	a, err := os.Stat(c.path(id))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("cache: %w", err)
	}

	if b, err := os.Stat(c.path(as)); err != nil || !os.SameFile(a, b) {
		return nil
	}
	return c.UnlinkLocal(id)
}

func (c *Cache) SwapLocal(ino meta.Ino, id string, gone meta.Ino, as string, commit func() error) error {

	if _, err := c.current(gone); err != nil {
		return err
	}

	c.mu.Lock()
	was := c.made[id]
	c.made[id] = true
	c.mu.Unlock()
	lock := c.fileLock(ino)
	lock.Lock()
	defer lock.Unlock()
	if err := commit(); err != nil {
		if !was {
			c.mu.Lock()
			delete(c.made, id)
			c.mu.Unlock()
		}
		return err
	}
	if err := c.replace(gone, as, nil); err != nil {
		return err
	}
	return c.MoveLocal(id, as)
}

func (c *Cache) MoveLocal(id, as string) error {
	if err := mkdirSynced(filepath.Dir(c.path(as))); err != nil {
		return fmt.Errorf("cache: %w", err)
	}
	c.takePath(c.path(as))
	c.mu.Lock()
	c.made[as] = true
	c.mu.Unlock()
	err := os.Rename(c.path(id), c.path(as))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("cache: %w", err)
	}
	c.mu.Lock()
	delete(c.made, id)
	c.mu.Unlock()
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err := syncDir(filepath.Dir(c.path(id))); err != nil {
		return err
	}
	return syncDir(filepath.Dir(c.path(as)))
}

func (c *Cache) Whole(ino meta.Ino, size int64) (bool, error) {
	g, err := c.current(ino)
	if err != nil {
		return false, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return !g.local && !g.stale && g.pieces.covers(span{0, size}), nil
}
