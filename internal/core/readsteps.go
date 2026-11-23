package core

import (
	"context"
	"errors"
	"syscall"

	"github.com/Ruslan-Linuxoid/FastGD/internal/cache"
	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func openAllowed(it meta.Item, flags uint32, writer bool) error {
	if writer && it.Denied.Has(meta.DenyModify) {
		return syscall.EACCES
	}

	whole := it.ContentLocal() && it.Base == 0
	anew := flags&syscall.O_ACCMODE == syscall.O_WRONLY && flags&syscall.O_TRUNC != 0
	if it.Denied.Has(meta.DenyDownload) && !whole && !anew {
		return syscall.EACCES
	}
	return nil
}

func (f *FS) cutOnOpen(ctx context.Context, it meta.Item, flags uint32, file cache.File) error {
	if flags&syscall.O_TRUNC == 0 {
		return nil
	}

	if file.Size == 0 {
		return f.SetMtime(uint64(it.Ino), f.clock.Now())
	}

	return f.Truncate(ctx, uint64(it.Ino), 0)
}

func (f *FS) readFresh(ctx context.Context, h *handle, dest []byte, off int64) (
	file cache.File, n int, done bool, err error,
) {
	it, err := f.settledItem(uint64(h.ino))
	switch {
	case err == nil && it.ContentLocal():

		f.edits.Flushed(it.Ino)
		n, err = f.readLocal(ctx, it, dest, off)
		return file, n, true, err
	case err == nil:
		file = f.edits.FileOf(it)
		return file, 0, off >= file.Size, nil

	case !h.r.Stale():
		return file, 0, true, err
	}
	return file, 0, false, nil
}

func (f *FS) readFailure(err error, ino meta.Ino, off int64) error {
	switch {
	case errors.Is(err, context.Canceled):
		return syscall.EINTR
	case errors.Is(err, cache.ErrStale):
		return syscall.EIO
	case errors.Is(err, drive.ErrAbusive):
		return syscall.EACCES
	case err != nil:
		return f.eio("read", err, "ino", ino, "off", off)
	}
	return nil
}

func (f *FS) settledItem(ino uint64) (meta.Item, error) {
	release := f.edits.CleanSettled()
	defer release()
	return f.item(ino)
}

func (f *FS) settledFile(ino uint64) (meta.Item, uint64, error) {
	release := f.edits.CleanSettled()
	defer release()
	it, err := f.item(ino)
	if err != nil {
		return it, 0, err
	}
	gen, err := f.cache.Generation(it.Ino)
	return it, gen, err
}
