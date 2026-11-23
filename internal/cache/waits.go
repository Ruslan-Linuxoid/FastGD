package cache

import (
	"context"
	"fmt"
	"os"
	"slices"
	"sync"
	"sync/atomic"
)

type fetch struct {
	span

	prefetch bool
	cancel   context.CancelFunc

	wanted     chan struct{}
	wantedOnce sync.Once

	mu sync.Mutex
	fd *os.File

	arrived int64

	tick  chan struct{}
	ended bool
	err   error

	refs int

	reserved atomic.Int64
	last     func()

	recorded bool

	reads int64
}

func (fl *fetch) isWanted() bool {
	select {
	case <-fl.wanted:
		return true
	default:
		return false
	}
}

func (fl *fetch) hold() {
	fl.mu.Lock()
	fl.refs++
	fl.mu.Unlock()
}

func (fl *fetch) release() {
	fl.mu.Lock()
	fl.refs--
	var fd *os.File
	var last func()
	if fl.refs == 0 {
		fd, fl.fd = fl.fd, nil
		last, fl.last = fl.last, nil
	}
	fl.mu.Unlock()
	if fd != nil {
		fd.Close()
	}
	if last != nil {
		last()
	}
}

func (fl *fetch) setFD(fd *os.File) {
	fl.mu.Lock()
	fl.fd = fd
	fl.mu.Unlock()
}

func (fl *fetch) progress(n int64) {
	fl.mu.Lock()
	fl.arrived += n
	close(fl.tick)
	fl.tick = make(chan struct{})
	fl.mu.Unlock()
}

func (fl *fetch) finish(err error) {
	fl.mu.Lock()
	fl.ended, fl.err = true, err
	close(fl.tick)
	fl.mu.Unlock()
}

func (fl *fetch) wait(ctx context.Context, upTo int64) error {
	fl.wantedOnce.Do(func() { close(fl.wanted) })
	for {
		fl.mu.Lock()
		if fl.off+fl.arrived >= upTo {
			fl.mu.Unlock()
			return nil
		}
		if fl.ended {
			err := fl.err
			fl.mu.Unlock()
			return err
		}
		tick := fl.tick
		fl.mu.Unlock()
		select {
		case <-tick:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (fl *fetch) waitEnd() error {
	for {
		fl.mu.Lock()
		ended, err, tick := fl.ended, fl.err, fl.tick
		fl.mu.Unlock()
		if ended {
			return err
		}
		<-tick
	}
}

func (fl *fetch) arrivedSpan() span {
	fl.mu.Lock()
	defer fl.mu.Unlock()
	return span{fl.off, fl.off + fl.arrived}
}

func (fl *fetch) readAt(p []byte, off int64) error {
	fl.mu.Lock()
	fd := fl.fd
	fl.mu.Unlock()
	if _, err := fd.ReadAt(p, off); err != nil {
		return fmt.Errorf("cache: read at %d: %w", off, err)
	}
	return nil
}

func (c *Cache) readClaimed(
	ctx context.Context, g *gen, f File, p []byte, off, end int64, in *atomic.Int64, waits []*fetch,
) (bool, error) {
	if err := c.waitFetches(ctx, g, waits, end); err != nil {
		return false, err
	}

	lock := c.fileLock(f.Ino)
	lock.RLock()
	defer lock.RUnlock()
	rd := &claimedRead{f: f, p: p, off: off, end: end, in: in}
	if stale := c.snapshot(g, rd, waits); stale {
		return false, errSwitched
	}
	for pos := off; pos < end; {
		var to int64
		var ok bool
		var err error
		if fl := fetchAt(waits, pos); fl != nil {
			to, ok, err = c.readFetched(rd, fl, pos)
		} else {
			to, ok, err = c.readRecorded(rd, pos)
		}
		if err != nil || !ok {
			return false, err
		}
		pos = to
	}
	return true, nil
}

type claimedRead struct {
	f        File
	p        []byte
	off, end int64
	in       *atomic.Int64

	recorded spans

	wasRecorded map[*fetch]bool
}

func (c *Cache) waitFetches(ctx context.Context, g *gen, waits []*fetch, end int64) error {
	for _, fl := range waits {
		if fl.off >= end {
			continue
		}
		if err := fl.wait(ctx, min(fl.end, end)); err != nil {

			return c.stopped(g, err, errSwitched)
		}
	}
	return nil
}

func (c *Cache) snapshot(g *gen, rd *claimedRead, waits []*fetch) (stale bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rd.recorded = slices.Clone(g.pieces)
	rd.wasRecorded = map[*fetch]bool{}
	for _, fl := range waits {
		rd.wasRecorded[fl] = fl.recorded
	}
	return g.stale
}

func (c *Cache) readFetched(rd *claimedRead, fl *fetch, pos int64) (to int64, ok bool, err error) {
	to = min(fl.end, rd.end)
	if rd.wasRecorded[fl] && !rd.recorded.covers(span{pos, to}) {

		return 0, false, nil
	}
	if err := fl.readAt(rd.p[pos-rd.off:to-rd.off], pos); err != nil {
		return 0, false, fmt.Errorf("cache: %s: %w", rd.f.ID, err)
	}

	if rd.in.Swap(fl.off) != fl.off {
		c.mu.Lock()
		late := fl.recorded
		if !late {
			fl.reads++
		}
		c.mu.Unlock()
		if late {
			c.worths.hit(rd.f.Ino, fl.off)
		}
	}
	return to, true, nil
}

func (c *Cache) readRecorded(rd *claimedRead, pos int64) (to int64, ok bool, err error) {
	s, ok := rd.recorded.at(pos)
	if !ok {
		return 0, false, nil
	}
	to = min(s.end, rd.end)
	if err := c.readSparse(rd.f.ID, rd.p[pos-rd.off:to-rd.off], pos); err != nil {
		return 0, false, err
	}
	if rd.in.Swap(s.off) != s.off {
		c.worths.hit(rd.f.Ino, s.off)
	}
	return to, true, nil
}

func fetchAt(fs []*fetch, pos int64) *fetch {
	for _, fl := range fs {
		if fl.off <= pos && pos < fl.end {
			return fl
		}
	}
	return nil
}
