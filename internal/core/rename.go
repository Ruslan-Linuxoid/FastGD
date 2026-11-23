package core

import (
	"errors"
	"syscall"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"golang.org/x/sys/unix"
)

func (f *FS) Rename(oldParent uint64, oldName string, newParent uint64, newName string, flags uint32) error {
	if flags&^unix.RENAME_NOREPLACE != 0 {

		return syscall.EINVAL
	}
	if err := checkName(newName); err != nil {
		return err
	}
	srcDir, src, err := f.child(oldParent, oldName)
	if err != nil {
		return err
	}

	defer f.dropNotes()
	dst, err := f.item(newParent)
	if err != nil {
		return err
	}
	mv, err := f.planMove(srcDir, src, dst, newName, flags)
	if err != nil {
		return err
	}
	return f.renameTo(mv)
}

func (f *FS) replaceable(src, target meta.Item) error {
	switch {
	case src.Kind == meta.KindDir && target.Kind != meta.KindDir:
		return syscall.ENOTDIR
	case src.Kind != meta.KindDir && target.Kind == meta.KindDir:
		return syscall.EISDIR
	case target.Kind == meta.KindDir:
		full, err := f.store.HasChildren(target.ID)
		if err != nil {
			return f.eio("rename", err, "id", target.ID)
		}
		if full {
			return syscall.ENOTEMPTY
		}
	}
	return nil
}

const loopAfter = 10000

func (f *FS) under(id, dir string) (bool, error) {
	for depth := 0; id != ""; depth++ {
		if id == dir {
			return true, nil
		}
		if depth > loopAfter {
			return false, syscall.ELOOP
		}
		it, err := f.store.ByID(id)
		if errors.Is(err, meta.ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, syscall.EIO
		}
		id = it.Parent
	}
	return false, nil
}

func checkMoveRights(src, from, to meta.Item, name string) error {
	if name != src.Name && src.Denied.Has(meta.DenyRename) {
		return syscall.EACCES
	}
	if to.ID == from.ID {
		return nil
	}

	if src.Kind == meta.KindShortcut || src.Denied.Has(meta.DenyMove) || from.Denied.Has(meta.DenyRemoveChildren) {
		return syscall.EACCES
	}

	if to.Denied.Has(meta.DenyAddChildren) {
		return syscall.EACCES
	}
	return nil
}
