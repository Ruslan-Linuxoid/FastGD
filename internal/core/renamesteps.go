package core

import (
	"errors"
	"syscall"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"golang.org/x/sys/unix"
)

type renaming struct {
	src, dst meta.Item
	newName  string
	name     string
	service  *bool
	placed   meta.Item
	flags    uint32
}

func (f *FS) planMove(srcDir, src, dst meta.Item, newName string, flags uint32) (renaming, error) {
	if err := f.checkPlaces(srcDir, src, dst); err != nil {
		return renaming{}, err
	}
	if err := f.roomForMove(src, srcDir, dst); err != nil {
		return renaming{}, err
	}
	if src.Kind == meta.KindFile && meta.IsLinkName(newName) {

		return renaming{}, syscall.EPERM
	}
	driveName, err := f.moveName(src, newName)
	if err != nil {
		return renaming{}, err
	}
	if err := f.checkMoveRights(src, srcDir, dst, driveName); err != nil {
		return renaming{}, err
	}

	service, err := f.store.ServiceAfterMove(src, dst, driveName)
	if errors.Is(err, meta.ErrHoldsDriveItems) {
		return renaming{}, syscall.EPERM
	}
	if err != nil {
		return renaming{}, f.eio("rename", err, "id", src.ID)
	}
	placed := meta.Item{Parent: dst.ID, Name: driveName, Mime: src.Mime}
	return renaming{src: src, dst: dst, newName: newName, name: driveName, service: service, placed: placed,
		flags: flags}, nil
}

func (f *FS) checkMoveInto(src, srcDir, dst meta.Item) error {
	if dst.Kind != meta.KindDir {
		return syscall.ENOTDIR
	}
	if err := meta.MayMove(src, srcDir, dst); err != nil {
		return errno(err)
	}
	if src.Kind != meta.KindDir {
		return nil
	}
	inside, err := f.under(dst.ID, src.ID)
	if err != nil {
		return err
	}
	if inside {
		return syscall.EINVAL
	}
	return nil
}

func (f *FS) roomForMove(src, srcDir, dst meta.Item) error {
	if dst.ID == srcDir.ID {
		return nil
	}
	levels := 0
	if src.Kind == meta.KindDir {
		height, err := f.store.Height(src.ID, f.opt.MaxDepth)
		if err != nil {
			return f.eio("rename", err, "id", src.ID)
		}
		levels = 1 + height
	}
	return f.roomIn(dst, levels)
}

func (f *FS) moveName(src meta.Item, newName string) (string, error) {
	driveName := meta.DriveNameOf(src, newName)
	shown, err := f.store.ShownNameOf(src)
	if err != nil {

		return "", f.eio("rename", err, "id", src.ID)
	}
	if shown == newName {

		driveName = src.Name
	}
	return driveName, nil
}

func (f *FS) renameTo(mv renaming) error {
	target, err := f.store.Lookup(mv.dst.ID, mv.newName)
	switch {
	case err == nil && target.Ino == mv.src.Ino:
		return nil
	case err == nil:
		return f.renameOver(mv, target)
	case !errors.Is(err, meta.ErrNotFound):
		return f.eio("rename", err, "name", mv.newName)
	}
	err = f.shown.local(func(v meta.Visible) error {
		if err := v.Rename(mv.src, mv.dst.ID, mv.name, mv.service); err != nil {
			return f.eio("rename", err, "id", mv.src.ID)
		}
		if mv.service != nil {
			f.edits.Recount()
		}
		return nil
	}, mv.src, mv.placed)
	if err == nil {

		if now, err := f.store.ShownNameOf(mv.src); err != nil || now != mv.newName {
			f.shown.named(mv.dst.Ino, mv.newName)
		}
	}
	return err
}

func (f *FS) renameOver(mv renaming, target meta.Item) error {
	if mv.flags&unix.RENAME_NOREPLACE != 0 {
		return syscall.EEXIST
	}
	if err := f.replaceable(mv.src, target); err != nil {
		return err
	}
	if mv.src.Service && mv.service == nil && target.RExists {

		mv.service = new(bool)
	}
	toDrive := !mv.src.Service
	if mv.service != nil {
		toDrive = !*mv.service
	}
	return f.shown.local(func(v meta.Visible) error {
		return f.replaceTarget(v, mv, target, toDrive)
	}, mv.src, mv.placed, target)
}

func (f *FS) replaceTarget(v meta.Visible, mv renaming, target meta.Item, toDrive bool) error {
	if toDrive {

		swapped, err := f.swapInto(v, mv.src, target, mv.dst.ID, mv.name)
		if err != nil {
			return f.eio("rename", err, "id", mv.src.ID)
		}
		if swapped {
			return nil
		}
	}
	if target.Denied.Has(meta.DenyTrash) {
		return syscall.EACCES
	}
	if err := v.Replace(mv.src, target.Ino, mv.dst.ID, mv.name, mv.service); err != nil {
		return f.eio("rename", err, "id", mv.src.ID)
	}
	f.edits.Recounted(target.Ino)
	f.forgetUnused(target.Ino)
	if mv.service != nil {
		f.edits.Recount()
	}
	return nil
}
