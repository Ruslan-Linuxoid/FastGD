package cache

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sys/unix"
)

const availShare = 5

const (
	driveShare = 10
	driveFloor = 1 << 30
)

const (
	limitEvery = time.Second
	keepEvery  = 10 * time.Second
)

const (
	freeEvery = 500 * time.Millisecond
	minFree   = 500 << 20
)

var ErrLowDisk = errors.New("cache: the disk of the cache is nearly full, nothing is downloaded")

type room struct {
	refreshing sync.Mutex
	mu         sync.Mutex

	used  int64
	limit int64

	dirty   int64
	checked time.Time

	said int64
	low  bool

	told   int64
	toldBy string
}

func (c *Cache) Used() int64 {
	c.room.mu.Lock()
	defer c.room.mu.Unlock()
	return c.room.used
}

func (c *Cache) Limit() int64 {
	c.room.mu.Lock()
	defer c.room.mu.Unlock()
	return c.room.limit
}

func (c *Cache) DirtyOver() int64 {
	c.refreshLimit(false)
	c.room.mu.Lock()
	defer c.room.mu.Unlock()
	return c.room.dirty - c.room.limit
}

func (c *Cache) spaceLimit(used, dirty int64) (limit int64, by string, err error) {
	free, err := c.opt.Free()
	if err != nil {
		return 0, "", fmt.Errorf("cache: free space: %w", err)
	}
	limit, by = c.opt.Size, "cache_size"
	if disk := (free + used + dirty) / availShare; disk < limit {
		limit, by = disk, "disk"
	}
	if files := c.driveFiles.Load(); files >= 0 {
		if drive := max(files/driveShare, driveFloor); drive < limit {
			limit, by = drive, "drive"
		}
	}
	return limit, by, nil
}

func (c *Cache) SetDriveFiles(n int64) {
	c.driveFiles.Store(n)
	c.refreshLimit(true)
}

const statBlock = 512

func (c *Cache) dirtyRoom() (int64, error) {
	ids, err := c.opt.Records.DirtyIDs()
	if err != nil {
		return 0, err
	}
	var n int64
	for _, id := range ids {
		var st unix.Stat_t
		if err := unix.Stat(c.path(id), &st); err == nil {
			n += st.Blocks * statBlock
		}
	}
	return n, nil
}

const limitSaidStep = 100

func (c *Cache) refreshLimit(force bool) {

	c.room.refreshing.Lock()
	defer c.room.refreshing.Unlock()
	used, due := c.limitDue(force)
	if !due {
		return
	}
	dirty, err := c.dirtyRoom()
	if err != nil {
		c.log.Warn("work out the cache limit", "err", err)
		return
	}
	limit, by, err := c.spaceLimit(used, dirty)
	if err != nil {
		c.log.Warn("work out the cache limit", "err", err)
		return
	}
	c.takeLimit(limit, dirty, by)
	if c.over() > 0 {
		c.trimSoon()
	}
}

func (c *Cache) limitDue(force bool) (used int64, due bool) {
	c.room.mu.Lock()
	defer c.room.mu.Unlock()
	due = force || c.dirtyMoved.Swap(false) || c.opt.Clock.Now().Sub(c.room.checked) >= limitEvery
	return c.room.used, due
}

func (c *Cache) over() int64 {
	evictable := c.evictable()
	c.room.mu.Lock()
	defer c.room.mu.Unlock()
	return evictable - c.room.limit
}

func (c *Cache) evictable() int64 {
	c.mu.Lock()
	var open int64
	for _, g := range c.gens {
		if g.refs == 0 || g.local || g.stale {
			continue
		}
		open += g.roomOf()
		for _, fl := range g.inflight {
			open += fl.reserved.Load()
		}
	}
	c.mu.Unlock()
	c.room.mu.Lock()
	defer c.room.mu.Unlock()
	return c.room.used - open
}

func (c *Cache) RoomFor(n int64) bool {
	return n+c.opt.MinFree <= c.free.Load()+max(c.evictable(), 0)
}

func (c *Cache) release(n int64) {
	c.room.mu.Lock()
	c.room.used -= n
	c.room.mu.Unlock()
}

func (c *Cache) take(n int64) {
	c.room.mu.Lock()
	c.room.used += n
	over := c.room.used > c.room.limit
	c.room.mu.Unlock()
	if over {
		c.trimSoon()
	}
}

func (c *Cache) trimSoon() {
	select {
	case c.trims <- struct{}{}:
	default:
	}
}

func (c *Cache) watchFree() {
	free, err := c.opt.Free()
	if err != nil {
		return
	}
	c.free.Store(free)
	if low := free < c.opt.MinFree; low != c.lowDisk.Swap(low) {
		if low {
			c.log.Warn("the disk of the cache is nearly full: nothing is downloaded until it has room, reads of what is not "+
				"cached fail",
				"free", free, "least", c.opt.MinFree)
		} else {
			c.log.Info("the disk of the cache has room again", "free", free)
		}
	}
	c.refreshLimit(false)
}

func statfsFree(dir string) func() (int64, error) {
	return func() (int64, error) {
		var st unix.Statfs_t
		if err := unix.Statfs(dir, &st); err != nil {
			return 0, err
		}

		return int64(st.Bavail) * st.Bsize, nil
	}
}
