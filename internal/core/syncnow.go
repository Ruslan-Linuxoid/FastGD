package core

import (
	"context"
	"errors"
	"time"
)

const idleLook = 100 * time.Millisecond

func (f *FS) SyncNow(ctx context.Context) (left int, err error) {
	if f.draining.Load() {
		return 0, errors.New("the daemon is stopping")
	}
	f.flushOpenFiles()

	if err := f.store.Hurry(); err != nil {
		return 0, err
	}
	for ctx.Err() == nil {

		if err := f.store.SkipDelays(); err != nil {
			return 0, err
		}

		if f.syncRound(ctx) > 0 {
			continue
		}
		if f.queue.idle() {
			break
		}
		f.waitIdle(ctx)
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return f.store.CountOps()
}

func (f *FS) flushOpenFiles() {
	for _, ino := range f.edits.Busy(f.clock.Now(), emptyIdleUpload) {
		it, err := f.store.ByIno(ino)
		if err != nil {
			continue
		}
		if err := f.edits.Sync(it); err != nil {
			f.log.Warn("flush a file for the sync asked for", "id", it.ID, "err", err)
		}
	}
}

func (f *FS) waitIdle(ctx context.Context) {
	select {
	case <-ctx.Done():
	case <-time.After(idleLook):
	}
}
