package cache

import (
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

const reclaimAfter = 2 * time.Minute

func (c *Cache) Reclaim() error {
	return filepath.WalkDir(c.opt.Dir, func(p string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:

			c.log.Warn("a part of the cache's directory cannot be read and is left as it is", "path", p, "err", err)
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		case d.IsDir():
			return nil
		}

		if err := c.reclaim(p, d); err != nil {
			c.log.Warn("a file of the cache's directory cannot be dealt with and is left as it is", "path", p, "err", err)
		}
		return nil
	})
}

const orphans = ".orphans"

func inOrphans(p string) bool { return filepath.Base(filepath.Dir(p)) == orphans }

func held(name string) bool { return strings.HasPrefix(name, HeldPrefix) }

func (c *Cache) CountStrays() error {
	return filepath.WalkDir(c.opt.Dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || held(d.Name()) {
			return err
		}
		if !inOrphans(p) {
			if known, err := c.opt.Records.Knows(d.Name()); err != nil || known {
				return err
			}
		}
		return c.stray(p)
	})
}

func (c *Cache) stray(p string) error {
	c.mu.Lock()
	_, counted := c.strays[p]
	if counted || c.made[filepath.Base(p)] {
		c.mu.Unlock()
		return nil
	}
	var st unix.Stat_t
	err := unix.Lstat(p, &st)
	if err != nil {
		c.mu.Unlock()
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}

	n := st.Blocks*statBlock + page()
	c.strays[p] = n
	c.mu.Unlock()
	c.take(n)
	return nil
}

func (c *Cache) takePath(p string) {
	c.mu.Lock()
	n, stray := c.strays[p]
	if stray {
		delete(c.strays, p)
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			c.log.Warn("content that no item has stays under the path an item takes", "path", p, "err", err)
		}
	}
	c.mu.Unlock()
	if stray {
		c.release(n)
	}
}

func (c *Cache) evictStrays(need int64) (int64, error) {
	c.mu.Lock()
	paths := slices.Sorted(maps.Keys(c.strays))
	c.mu.Unlock()
	var freed int64
	for _, p := range paths {
		if freed >= need {
			break
		}
		c.mu.Lock()
		n, stray := c.strays[p]
		var err error
		if stray {
			if err = os.Remove(p); errors.Is(err, fs.ErrNotExist) {
				err = nil
			}
			if err == nil {
				delete(c.strays, p)
			}
		}
		c.mu.Unlock()
		if err != nil {
			return freed, err
		}
		if !stray {
			continue
		}
		if inOrphans(p) {

			_ = os.Remove(filepath.Dir(p))
		}
		c.release(n)
		freed += n
	}
	return freed, nil
}

func (c *Cache) reclaim(p string, d fs.DirEntry) error {
	c.mu.Lock()
	made := c.made[d.Name()]
	c.mu.Unlock()
	if made || held(d.Name()) {
		return nil
	}
	if inOrphans(p) {
		return c.stray(p)
	}
	it, err := c.opt.Records.ByID(d.Name())
	if errors.Is(err, meta.ErrNotFound) {
		return c.stray(p)
	}
	if err != nil || it.ContentLocal() {
		return err
	}
	lock := c.fileLock(it.Ino)
	lock.Lock()
	defer lock.Unlock()
	c.mu.Lock()
	_, used := c.gens[it.Ino]
	c.mu.Unlock()
	if used {
		return nil
	}
	return c.trimToRecords(p, d, it)
}

func (c *Cache) trimToRecords(p string, d fs.DirEntry, it meta.Item) error {
	fi, err := d.Info()
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	recs, err := c.opt.Records.Pieces(it.Ino)
	if err != nil {
		return err
	}
	if len(recs) == 0 {
		return os.Remove(p)
	}
	var recorded spans
	for _, r := range recs {
		recorded.add(pieceSpan(r.Off, r.Len))
	}
	for _, gap := range recorded.gaps(span{0, fi.Size()}) {
		if err := c.punch(p, gap); err != nil {
			return fmt.Errorf("cache: reclaim %s: %w", d.Name(), err)
		}
	}
	return nil
}
