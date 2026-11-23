package core

import (
	"context"
	"errors"
	"syscall"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/cache"
	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/edited"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func isWrite(flags uint32) bool {
	acc := flags & syscall.O_ACCMODE
	return acc == syscall.O_WRONLY || acc == syscall.O_RDWR
}

func (f *FS) Create(parent uint64, name string, flags uint32) (Attr, uint64, error) {
	it, err := f.create(parent, name, meta.KindFile)
	if err != nil {
		return Attr{}, 0, err
	}
	if err := f.cache.CreateLocal(it.ID); err != nil {
		return Attr{}, 0, f.eio("create", err, "name", name)
	}

	fh, err := f.open(context.Background(), it, 0, flags)
	if err != nil {
		return Attr{}, 0, err
	}
	return f.attr(it), fh, nil
}

func (f *FS) Mkdir(parent uint64, name string) (Attr, error) {
	it, err := f.create(parent, name, meta.KindDir)
	if err != nil {
		return Attr{}, err
	}
	return f.attr(it), nil
}

func (f *FS) create(parent uint64, name string, kind meta.Kind) (meta.Item, error) {
	p, err := f.item(parent)
	if err != nil {
		return meta.Item{}, err
	}
	if err := f.checkCreateName(p, name, kind); err != nil {
		return meta.Item{}, err
	}
	if err := f.checkCreateRights(p, kind); err != nil {
		return meta.Item{}, err
	}
	now := edited.DriveTime(f.clock.Now())
	driveName := meta.DriveName(name)
	mime := drive.MimeFolder
	if kind == meta.KindFile {
		mime = drive.MimeFor(driveName)
	}

	made := meta.Item{Kind: kind, Mime: mime, Name: driveName, Parent: p.ID, Mtime: now, Ctime: now,
		Service: meta.ServiceIn(p, driveName)}
	if kind == meta.KindFile {

		made.UploadAfter = f.clock.Now().Add(emptyIdleUpload)
	}

	var it meta.Item
	err = f.shown.made(func(v meta.Visible) (err error) {
		it, err = v.Create(made)
		return err
	})
	if err != nil {
		return meta.Item{}, f.eio("create", err, "name", name)
	}
	return it, nil
}

func (f *FS) checkCreateName(p meta.Item, name string, kind meta.Kind) error {
	if p.Kind != meta.KindDir {
		return syscall.ENOTDIR
	}
	if err := checkName(name); err != nil {
		return err
	}
	if kind == meta.KindFile && meta.IsLinkName(name) {

		return syscall.EPERM
	}
	if _, err := f.store.Lookup(p.ID, name); err == nil {
		return syscall.EEXIST
	}
	return nil
}

func (f *FS) checkCreateRights(p meta.Item, kind meta.Kind) error {
	if err := meta.MayCreateIn(p); err != nil {
		return errno(err)
	}
	if p.Denied.Has(meta.DenyAddChildren) {
		return syscall.EACCES
	}
	levels := 0
	if kind == meta.KindDir {
		levels = 1
	}
	return f.roomIn(p, levels)
}

func (f *FS) Write(ctx context.Context, fh uint64, off int64, data []byte) (int, error) {
	h, ok := f.handles.get(fh)
	if !ok || !h.writer {
		return 0, syscall.EBADF
	}
	it, err := f.settledItem(uint64(h.ino))
	if err != nil {
		return 0, err
	}

	need := int64(len(data)) + 2*cache.HoleUnit()
	if !it.ContentLocal() {
		need = int64(len(data)) + it.Size
	}
	if !f.cache.RoomFor(need) {
		if !f.noRoom.Swap(true) {
			f.log.Warn("no room on the disk of the cache: writes into the mount get ENOSPC until it has room", "id", it.ID,
				"bytes", len(data))
		}
		return 0, syscall.ENOSPC
	}
	f.noRoom.Store(false)
	if err := f.edits.Write(ctx, it, off, data); err != nil {
		return 0, f.editErrno(ctx, "write", it, err)
	}
	return len(data), nil
}

func (f *FS) editErrno(ctx context.Context, what string, it meta.Item, err error) error {
	if ctx.Err() != nil {
		return syscall.EINTR
	}

	en, refused := err.(syscall.Errno)
	switch {
	case refused:
		return en
	case errors.Is(err, edited.ErrDenied):
		return syscall.EACCES
	case errors.Is(err, meta.ErrNotFound):
		return errno(err)
	}
	return f.eio(what, err, "id", it.ID)
}

func (f *FS) Truncate(ctx context.Context, ino uint64, size int64) error {
	it, err := f.settledItem(ino)
	if err != nil {
		return err
	}
	if it, err = f.edits.TakenUp(it); err != nil {
		return f.eio("truncate", err, "id", it.ID)
	}
	if err := truncateAllowed(it, size); err != nil {
		return err
	}
	if err := f.edits.Truncate(ctx, it, size); err != nil {
		return f.editErrno(ctx, "truncate", it, err)
	}
	return nil
}

func truncateAllowed(it meta.Item, size int64) error {
	if it.Denied.Has(meta.DenyDownload) && size != 0 && (!it.ContentLocal() || it.Base != 0) {

		return syscall.EACCES
	}

	switch it.Kind {
	case meta.KindFile:
	case meta.KindDoc:
		return syscall.EACCES
	default:
		return syscall.EISDIR
	}
	return nil
}

func (f *FS) Fsync(fh uint64) error {
	h, ok := f.handles.get(fh)
	if !ok {
		return syscall.EBADF
	}
	if f.edits.StateIf(h.ino) == nil {
		return nil
	}
	it, err := f.item(uint64(h.ino))
	if err != nil {
		return err
	}
	if err := f.edits.Sync(it); err != nil {
		return f.eio("fsync", err, "id", it.ID)
	}
	return nil
}

func (f *FS) SetMtime(ino uint64, mtime time.Time) error {
	mtime = edited.DriveTime(mtime)
	it, err := f.item(ino)
	if err != nil {
		return err
	}
	if it.Kind == meta.KindDoc || it.Denied.Has(meta.DenyModify) {

		return syscall.EACCES
	}

	if err := f.store.SetMtime(it.Ino, mtime); err != nil {
		return f.eio("set mtime", err, "id", it.ID)
	}
	f.edits.SetMtime(it.Ino, mtime)
	return nil
}
