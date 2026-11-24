package core

import (
	"errors"
	"strconv"
	"syscall"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"sync/atomic"
)

const uploadedRecount = time.Minute

func (f *FS) room(it meta.Item, old, size int64) error {
	added := size - old
	if it.LocalOnly() || added <= 0 {

		return nil
	}
	if why := f.refused.Load(); why != nil {
		f.readOnly(it, *why)
		return syscall.EROFS
	}
	dirty := f.edits.Bytes() + added
	if left := f.opt.DailyUpload - f.uploadedToday(); dirty > left {
		f.readOnly(it, "the dirty files would not fit what the daily upload limit leaves", "dirty", dirty, "left", left)
		return syscall.EROFS
	}
	if q := f.quota.Load(); q != nil && q.Limit > 0 && dirty > q.Limit-q.Usage {
		f.readOnly(it, "the dirty files would not fit the free space in Drive", "dirty", dirty, "free", q.Limit-q.Usage)
		return syscall.EROFS
	}
	return nil
}

func (f *FS) readOnly(it meta.Item, why string, args ...any) {
	if switched(&f.lastReadOnlyReason, why) {
		f.log.Warn("the mount takes no more content until Drive can take it",
			append([]any{"id", it.ID, "why", why}, args...)...)
	}
}

func (f *FS) uploadedToday() int64 {
	f.upMu.Lock()
	defer f.upMu.Unlock()
	now := f.clock.Now()
	if now.Sub(f.upAt) >= uploadedRecount {
		n, err := f.store.UploadedSince(now.Add(-drive.QuotaWindow))
		if err != nil {
			f.log.Warn("count the bytes uploaded today", "err", err)
			return f.upSum
		}
		f.upSum, f.upAt = n, now
	}
	return f.upSum
}

func (f *FS) countUpload(size int64) {
	f.upMu.Lock()
	defer f.upMu.Unlock()
	now := f.clock.Now()
	if err := f.store.RecordUpload(now, size, now.Add(-drive.QuotaWindow)); err != nil {
		f.log.Warn("count an upload", "err", err)
	}
	f.upSum += size
}

func (f *FS) refuse(why string) {
	if switched(&f.refused, why) {
		f.log.Error("Drive takes no more uploads; writes of content get EROFS until an upload goes through", "why", why)
		if err := f.store.SetState(meta.StateRefused, why); err != nil {
			f.log.Warn("keep the refusal of uploads", "err", err)
		}
	}
}

func (f *FS) liftIdleRefusal() {
	if f.refused.Load() == nil {
		return
	}
	if left, err := f.store.HasOps(); err != nil || left {
		return
	}
	f.log.Info("nothing is left to upload, so nothing tells whether Drive takes uploads: writes are let through")
	f.accepted()
}

func (f *FS) accepted() {
	if f.refused.Swap(nil) != nil {
		f.log.Info("Drive takes uploads again")
		f.lastReadOnlyReason.Store(nil)
		if err := f.store.DeleteState(meta.StateRefused); err != nil {
			f.log.Warn("drop the refusal of uploads", "err", err)
		}
	}
}

func (f *FS) signedOut(err error) {
	if !errors.Is(err, drive.ErrLoginExpired) || f.noLogin.Swap(true) {
		return
	}
	f.log.Error("Drive does not take the sign-in: changes made here wait, and nothing is undone, until the account "+
		"signs in again", "err", err)
	if err := f.store.SetState(meta.StateLoginNeeded, strconv.FormatInt(f.clock.Now().UnixNano(), 10)); err != nil {
		f.log.Warn("keep the need to sign in", "err", err)
	}
}

func (f *FS) signedIn() {
	if !f.noLogin.Swap(false) {
		return
	}
	f.log.Info("Drive takes the sign-in again")
	if err := f.store.DeleteState(meta.StateLoginNeeded); err != nil {
		f.log.Warn("drop the need to sign in", "err", err)
	}

	if err := f.store.RetryNetFailures(); err != nil {
		f.log.Warn("bring forward what waited for the sign-in", "err", err)
	}
}

func (f *FS) acceptedBytes(size int64) {
	if size > 0 {
		f.accepted()
	}
}

func (f *FS) loadRefusal() {
	if _, ok, err := f.store.GetState(meta.StateLoginNeeded); err == nil && ok {
		f.noLogin.Store(true)
	}
	if why, ok, err := f.store.GetState(meta.StateRefused); err == nil && ok {
		f.refused.Store(&why)
	}
}

func limitBackoff(n int) time.Duration {
	return backoffIn(n, time.Minute, longestWait)
}

func (f *FS) roomIn(parent meta.Item, levels int) error {
	maxChildren, maxDepth := f.opt.MaxChildren, f.opt.MaxDepth

	n := 0
	if !meta.IsVirtual(parent.Parent) {
		var err error
		if n, err = f.store.ChildCount(parent.ID, maxChildren); err != nil {
			return f.eio("count the items of a folder", err, "id", parent.ID)
		}
	}
	if n >= maxChildren {
		f.warnRefused(refusal{id: parent.ID, right: "items"}, "a Drive folder holds no more items",
			"folder", parent.ID, "items", n, "limit", maxChildren)
		return syscall.ENOSPC
	}
	if levels == 0 {
		return nil
	}
	depth, err := f.store.Depth(parent.ID, maxDepth)
	if err != nil {
		return f.eio("depth of a folder", err, "id", parent.ID)
	}
	if depth+levels > maxDepth {
		f.warnRefused(refusal{id: parent.ID, right: "depth"}, "Drive keeps folders no deeper",
			"folder", parent.ID, "depth", depth+levels, "limit", maxDepth)
		return syscall.ENOSPC
	}
	return nil
}

func (f *FS) reconnected() {
	if f.closed.Load() {
		return
	}
	if err := f.store.RetryNetFailures(); err != nil {
		f.log.Warn("bring forward the uploads that failed on the network", "err", err)
		return
	}
	f.log.Info("the network is back; the uploads that failed on it go now")
}

const dirtyOverEvery = 10 * time.Minute

func (f *FS) warnDirtyOver() {
	over := f.cache.DirtyOver()
	now := f.clock.Now()
	switch {
	case over <= 0:
		f.dirtyOverWarnedAt = time.Time{}
	case f.dirtyOverWarnedAt.IsZero() || now.Sub(f.dirtyOverWarnedAt) >= dirtyOverEvery:
		f.log.Warn("the dirty files take more than the cache limit until they are uploaded", "over", over, "limit",
			f.cache.Limit())
		f.dirtyOverWarnedAt = now
	}
}

func switched(p *atomic.Pointer[string], v string) bool {
	last := p.Swap(&v)
	return last == nil || *last != v
}
