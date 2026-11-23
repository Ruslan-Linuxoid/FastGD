package cache

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/Ruslan-Linuxoid/FastGD/internal/clock"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func New(o Options) (*Cache, error) {
	if err := mkdirSynced(o.Dir); err != nil {
		return nil, fmt.Errorf("cache: %w", err)
	}
	if o.punchHoleAt == nil {
		o.punchHoleAt = punchHole
	}
	found, first, err := agreeUnit(o)
	if err != nil {
		return nil, err
	}
	opened := false
	defer func() {

		if first && !opened {
			unit.Store(0)
		}
	}()
	logUnit(o, found)
	if err := removeHeld(o.Dir); err != nil {
		return nil, err
	}
	setDefaults(&o)
	pieces, err := o.Records.CachedPieces()
	if err != nil {
		return nil, err
	}
	c, err := openRoom(o, cleanRoom(pieces))
	if err != nil {
		return nil, err
	}
	c.wg.Add(1)
	go c.keep()
	opened = true
	return c, nil
}

func agreeUnit(o Options) (found int64, first bool, err error) {
	found, err = holeUnit(o.Dir, o.punchHoleAt)
	if err != nil {
		return 0, false, fmt.Errorf("cache: %w", err)
	}
	if o.HoleUnit != 0 {
		if o.HoleUnit < found || o.HoleUnit%found != 0 || !countable(o.HoleUnit) {
			return 0, false, fmt.Errorf("cache: the unit of holes asked for, %d, is no multiple of the file system's %d that "+
				"the cache can count by", o.HoleUnit, found)
		}
		found = o.HoleUnit
	}
	first = unit.CompareAndSwap(0, found)
	if !first && unit.Load() != found {
		return 0, false, fmt.Errorf("cache: %s keeps holes by %d bytes, and a cache of this process counts by %d", o.Dir,
			found, unit.Load())
	}
	return found, first, nil
}

func logUnit(o Options, found int64) {
	if found != defaultUnit {
		o.Log.Warn("the file system of the cache keeps the holes of a file by units other than 4 KiB: the cache counts by "+
			"them", "dir", o.Dir, "unit", found, "portion", portionOf(found))
	} else {
		o.Log.Info("the file system of the cache keeps the holes of a file by 4 KiB", "dir", o.Dir, "portion",
			portionOf(found))
	}
}

func removeHeld(dir string) error {
	held, _ := filepath.Glob(filepath.Join(dir, HeldPrefix+"*"))
	for _, p := range held {
		if err := os.Remove(p); err != nil {
			return fmt.Errorf("cache: %w", err)
		}
	}
	return nil
}

func setDefaults(o *Options) {
	if o.Free == nil {
		o.Free = statfsFree(o.Dir)
	}
	if o.MinFree == 0 {
		o.MinFree = minFree
	}
	if o.Clock == nil {
		o.Clock = clock.Real{}
	}
}

func openRoom(o Options, used int64) (*Cache, error) {
	bg, stop := context.WithCancel(context.Background())
	c := &Cache{
		opt:      o,
		log:      o.Log,
		bg:       bg,
		stopBg:   stop,
		sem:      make(chan struct{}, prefetchWorkers),
		gens:     map[meta.Ino]*gen{},
		locks:    map[meta.Ino]*sync.RWMutex{},
		trims:    make(chan struct{}, 1),
		made:     map[string]bool{},
		strays:   map[string]int64{},
		fetching: map[*gen]struct{}{},
		worths:   worths{of: map[meta.Ino]map[int64]*worth{}, gone: map[pieceAt]int64{}, local: map[meta.Ino]int64{}},
	}
	c.driveFiles.Store(-1)
	c.meter.SetLimit(o.MaxSpeed)
	dirty, err := c.dirtyRoom()
	if err != nil {
		stop()
		return nil, err
	}
	free, err := o.Free()
	if err != nil {
		stop()
		return nil, fmt.Errorf("cache: free space: %w", err)
	}
	if avail := free + used + dirty; avail/availShare < o.MinLimit {
		stop()
		return nil, fmt.Errorf("cache: %s has %d bytes for the cache, a fifth of which, %d, is less than the least cache "+
			"of %d", o.Dir, avail, avail/availShare, o.MinLimit)
	}
	c.room.used = used
	c.free.Store(free)
	c.refreshLimit(true)
	return c, nil
}
