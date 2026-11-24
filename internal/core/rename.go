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

func (f *FS) checkMoveRights(src, from, to meta.Item, name string) error {
	if name != src.Name && src.Denied.Has(meta.DenyRename) {
		return f.lacksRight("rename the item", src, "canRename")
	}
	if to.ID == from.ID {
		return nil
	}

	switch {
	case src.Kind == meta.KindShortcut:
		return syscall.EACCES
	case src.Denied.Has(meta.DenyMove):
		return f.lacksRight("move the item", src, "canMoveItemWithinDrive")
	case from.Denied.Has(meta.DenyRemoveChildren):
		return f.lacksRight("take an item out of the folder", from, "canRemoveChildren")
	case to.Denied.Has(meta.DenyAddChildren):

		return f.lacksRight("add an item to the folder", to, "canAddChildren")
	}
	return nil
}

func (f *FS) checkSections(src, from, to meta.Item) error {
	if to.ID == from.ID {
		return nil
	}
	a, err := f.store.SectionOf(from.ID)
	if err != nil {
		return f.eio("the part of the mount of a folder", err, "id", from.ID)
	}
	b, err := f.store.SectionOf(to.ID)
	if err != nil {
		return f.eio("the part of the mount of a folder", err, "id", to.ID)
	}
	switch {
	case a == b:
		return nil
	case a == meta.SharedWithMeID || b == meta.SharedWithMeID || src.Denied.Has(meta.DenyOutOfDrive):
		return syscall.EXDEV
	}
	return nil
}

func (f *FS) checkPlaces(srcDir, src, dst meta.Item) error {

	if meta.Fixed(src) || meta.FixedIn(srcDir) || meta.FixedIn(dst) {
		return syscall.EPERM
	}
	if err := f.checkMoveInto(src, srcDir, dst); err != nil {
		return err
	}
	return f.checkSections(src, srcDir, dst)
}

func (f *FS) lacksRight(what string, it meta.Item, right string) error {
	f.warnRefused(refusal{id: it.ID, right: right, what: what}, "Drive does not let the user "+what,
		"name", it.Name, "id", it.ID, "lacks", right)
	return syscall.EACCES
}
