package core

import (
	"log/slog"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/cache"
	"github.com/Ruslan-Linuxoid/FastGD/internal/clock"
	"github.com/Ruslan-Linuxoid/FastGD/internal/edited"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"github.com/Ruslan-Linuxoid/FastGD/internal/speed"
)

func DBPath(cacheDir string) string { return filepath.Join(cacheDir, "meta.db") }

func ContentDir(cacheDir string) string { return filepath.Join(cacheDir, "content") }

type FS struct {
	budget

	opt   Options
	store *meta.Store
	cache *cache.Cache

	edits   *edited.Files
	log     *slog.Logger
	clock   clock.Clock
	handles handles

	shown *shown

	noRoom atomic.Bool

	conflicts *conflicts

	pollMu pollLock

	wmu sync.Mutex

	upload chan struct{}

	queue *queue

	sent map[meta.Ino]sent

	idsWanted chan struct{}

	bg sync.WaitGroup

	gone goneRevisions

	pollSaved time.Time

	upMeter speed.Meter

	refusals *refusals

	noLogin atomic.Bool

	retries retries

	closed atomic.Bool

	beforeChunk func(off int64)

	beforeOpen func()

	draining atomic.Bool

	starting sync.Mutex

	distrusted   chan struct{}
	distrustOnce sync.Once
}

func (f *FS) Close() error {
	wasClosed := f.closed.Swap(true)
	f.opt.Drive.OnReconnect(nil)
	f.cache.Close()
	if !wasClosed {
		f.saveUsage()
	}
	f.shown.telling.Wait()
	return f.store.Close()
}

func (f *FS) eio(op string, err error, kv ...any) error {
	f.log.Error(op, append(kv, "err", err)...)
	return syscall.EIO
}

func (f *FS) recountByUnit() error {
	now := strconv.FormatInt(cache.HoleUnit(), 10)
	had, told, err := f.store.GetState(meta.StateHoleUnit)
	if err != nil {
		return err
	}
	if !told {

		had = "4096"
	}
	if had != now {
		pieces, err := f.store.CachedPieces()
		if err != nil {
			return err
		}
		dropped := map[meta.Ino]bool{}
		for _, p := range pieces {
			if dropped[p.Ino] {
				continue
			}
			dropped[p.Ino] = true
			if err := f.cache.Invalidate(p.Ino, p.ID); err != nil {
				return err
			}
		}
		f.log.Warn("the cache counts by another unit of holes than its records were made by: the cached content of clean "+
			"files is dropped", "was", had, "unit", now, "files", len(dropped))
	}
	if told && had == now {
		return nil
	}
	return f.store.SetState(meta.StateHoleUnit, now)
}

func (f *FS) dropOldLayout() error {
	if _, pending, err := f.store.GetState(meta.StateDropClean); err != nil || !pending {
		return err
	}
	kept, err := f.cache.DropOldLayout()
	if err != nil {
		return err
	}
	f.log.Info("dropped the clean content of the older cache layout", "kept_dirty", kept)
	return f.store.DeleteState(meta.StateDropClean)
}
