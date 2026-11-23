package core

import (
	"cmp"
	"context"
	"errors"
	"fmt"

	"github.com/Ruslan-Linuxoid/FastGD/internal/cache"
	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func (f *FS) dropClean(old, changed meta.Item) error {
	return f.dropCached(old, f.cache.Invalidate, func(now meta.Item) bool {
		return now.ContentLocal() || !contentChanged(now, changed)
	})
}

func (f *FS) dropCached(old meta.Item, drop func(meta.Ino, string) error, keep func(now meta.Item) bool) error {
	now, err := f.store.ByID(old.ID)
	if errors.Is(err, meta.ErrNotFound) || err == nil && keep(now) {
		return nil
	}
	if err != nil {

		return fmt.Errorf("read the item of %s before its cached content goes: %w", old.ID, err)
	}
	if err := drop(old.Ino, old.ID); err != nil {
		return fmt.Errorf("drop cached content of %s: %w", old.ID, err)
	}
	return nil
}

func (f *FS) removeClean(old meta.Item) error {
	return f.dropCached(old, f.cache.Remove, meta.Item.AheadOfDrive)
}

func contentChanged(before, after meta.Item) bool {
	return after.Kind == meta.KindFile && after.Version > before.Version &&
		(before.MD5 != after.MD5 || before.Size != after.Size)
}

func (f *FS) mixed(ctx context.Context, cf cache.File) {
	rerr := f.readAnew(ctx, cf.ID)

	release := f.edits.HoldClean()
	it, err := f.store.ByIno(cf.Ino)
	if err == nil && !it.Dirty && !it.ContentLocal() && it.MD5 == cf.MD5 {
		err = f.cache.Invalidate(cf.Ino, cf.ID)
	}
	release()
	if err = cmp.Or(rerr, err); err != nil && ctx.Err() == nil {
		f.log.Warn("read anew a file whose cached content was of another version", "id", cf.ID, "err", err)
	}
}

func (f *FS) readAnew(ctx context.Context, id string) error {
	rf, err := f.opt.Drive.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := f.pollMu.LockCtx(ctx); err != nil {
		return err
	}
	defer f.pollMu.Unlock()
	token, _, err := f.store.GetState(meta.StateChangesToken)
	if err != nil {
		return err
	}
	return f.applyChanges(ctx, []drive.Change{{FileID: id, File: rf}}, token)
}
