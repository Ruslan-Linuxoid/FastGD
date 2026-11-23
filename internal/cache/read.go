package cache

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync/atomic"
)

type Reader struct {
	c   *Cache
	g   *gen
	win window

	pinned File

	in atomic.Int64
}

func (c *Cache) Open(f File) (*Reader, error) {
	for {
		g, err := c.current(f.Ino)
		if err != nil {
			return nil, err
		}
		c.mu.Lock()
		if c.gens[f.Ino] == g && f.Gen != 0 && f.Gen != g.num {
			c.mu.Unlock()
			return nil, ErrStale
		}
		if c.gens[f.Ino] == g {
			g.refs++
			c.mu.Unlock()
			r := &Reader{c: c, g: g, pinned: f}
			r.in.Store(-1)
			return r, nil
		}

		c.mu.Unlock()
	}
}

func (r *Reader) Stale() bool { return r.c.isStale(r.g) }

func (r *Reader) Close() { r.c.unref(r.g) }

func (c *Cache) unref(g *gen) {
	c.mu.Lock()
	g.refs--
	var fd *os.File
	if g.stale && g.refs == 0 {
		fd, g.fd = g.fd, nil
	}
	c.mu.Unlock()
	if fd != nil {
		fd.Close()
	}
}

func (r *Reader) ReadAt(ctx context.Context, f File, p []byte, off int64) (int, error) {
	if !r.Stale() {

		planned := func(hit bool, end int64) {
			r.c.prefetch(r.g, f, r.win.plan(off, end, hit, r.c.WindowCapOf(f.Ino)))
		}
		n, err := r.c.readCurrent(ctx, r.g, f, p, off, &r.in, planned)
		if !errors.Is(err, errSwitched) {
			return n, err
		}
	}
	return r.readStale(p, off)
}

func (r *Reader) readStale(p []byte, off int64) (int, error) {
	c, f := r.c, r.pinned
	if off >= f.Size || len(p) == 0 {
		return 0, nil
	}
	end := min(off+int64(len(p)), f.Size)
	c.mu.Lock()
	fd := r.g.fd
	covered := r.g.pieces.covers(span{off, end})
	c.mu.Unlock()
	if !covered || fd == nil {
		return 0, ErrStale
	}

	if _, err := fd.ReadAt(p[:end-off], off); err != nil {
		return 0, fmt.Errorf("cache: read %s at %d: %w", f.ID, off, err)
	}
	return int(end - off), nil
}

func (c *Cache) readCurrent(
	ctx context.Context, g *gen, f File, p []byte, off int64, in *atomic.Int64, planned func(hit bool, end int64),
) (int, error) {
	if off >= f.Size || len(p) == 0 {
		return 0, nil
	}
	end := min(off+int64(len(p)), f.Size)
	for healed := false; ; healed = true {
		err := c.readBase(ctx, g, f, p, off, end, in, planned)
		planned = nil
		if errors.Is(err, fs.ErrNotExist) && !healed {

			c.log.Warn("cached file is gone, fetching it again", "id", f.ID)
			if err := c.forget(g, f.Ino); err != nil {
				return 0, err
			}
			continue
		}
		if err != nil {
			return 0, err
		}
		break
	}
	if local := max(off, f.Base); local < end {
		if err := c.readSparse(f.ID, p[local-off:end-off], local); err != nil {
			return 0, err
		}
	}
	return int(end - off), nil
}

func (c *Cache) readBase(
	ctx context.Context, g *gen, f File, p []byte, off, end int64, in *atomic.Int64, planned func(bool, int64),
) error {
	baseEnd := min(end, f.Base)
	if off >= baseEnd {
		if planned != nil {
			planned(true, end)
		}
		return nil
	}
	want := pageSpan(off, baseEnd, f.Base)
	if err := c.refuseOnLowDisk(g, want); err != nil {
		return err
	}
	for try := 1; ; try++ {

		waits, started, err := c.claim(g, f, want)
		if err != nil {
			return err
		}
		if planned != nil {
			planned(!started, end)
			planned = nil
		}
		done, err := c.readClaimed(ctx, g, f, p, off, baseEnd, in, waits)
		releaseAll(waits)
		if err != nil || done {
			return err
		}
		if try == readTries {
			return fmt.Errorf("cache: the pieces of %s at %d keep vanishing", f.ID, off)
		}
	}
}

func (c *Cache) refuseOnLowDisk(g *gen, want span) error {
	if !c.lowDisk.Load() {
		return nil
	}
	c.mu.Lock()
	lacking := len(g.missing(want)) > 0
	c.mu.Unlock()
	if lacking {
		return ErrLowDisk
	}
	return nil
}

func releaseAll(waits []*fetch) {
	for _, fl := range waits {
		fl.release()
	}
}

func (c *Cache) readSparse(id string, p []byte, off int64) error {
	fd, err := os.Open(c.path(id))
	if err != nil {
		return fmt.Errorf("cache: %w", err)
	}
	defer fd.Close()
	if _, err := fd.ReadAt(p, off); err != nil {
		return fmt.Errorf("cache: read %s at %d: %w", id, off, err)
	}
	return nil
}

func (c *Cache) stopped(g *gen, err, stale error) error {
	if c.isStale(g) {
		return stale
	}
	if c.isLocal(g) {
		return ErrLocal
	}
	return err
}
