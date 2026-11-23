package core

import (
	"errors"
	"syscall"
	"unicode/utf8"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func (f *FS) child(parent uint64, name string) (dir, it meta.Item, err error) {
	if dir, err = f.item(parent); err != nil {
		return meta.Item{}, meta.Item{}, err
	}
	if dir.Kind != meta.KindDir {
		return meta.Item{}, meta.Item{}, syscall.ENOTDIR
	}
	if it, err = f.store.Lookup(dir.ID, name); err != nil {
		if !errors.Is(err, meta.ErrNotFound) {
			f.log.Error("lookup", "parent", dir.ID, "name", name, "err", err)
		}
		return dir, meta.Item{}, errno(err)
	}
	return dir, it, nil
}

func (f *FS) Unlink(parent uint64, name string) error {
	_, it, err := f.child(parent, name)
	if err != nil {
		return err
	}
	if it.Kind == meta.KindDir {
		return syscall.EISDIR
	}
	return f.shown.local(func(v meta.Visible) error { return f.trash(v, it) }, it)
}

func (f *FS) Rmdir(parent uint64, name string) error {
	_, it, err := f.child(parent, name)
	if err != nil {
		return err
	}
	if it.Kind != meta.KindDir {
		return syscall.ENOTDIR
	}
	full, err := f.store.HasChildren(it.ID)
	if err != nil {
		return f.eio("rmdir", err, "id", it.ID)
	}
	if full {
		return syscall.ENOTEMPTY
	}
	return f.shown.local(func(v meta.Visible) error { return f.trash(v, it) }, it)
}

func (f *FS) trash(v meta.Visible, it meta.Item) error {
	if it.Denied.Has(meta.DenyTrash) {

		return syscall.EACCES
	}
	if err := v.Trash(it.Ino); err != nil {
		return f.eio("trash", err, "id", it.ID)
	}
	f.edits.Recounted(it.Ino)
	f.forgetUnused(it.Ino)
	return nil
}

func (f *FS) dropNotes() {
	inos, err := f.store.TrashedNotes()
	if err != nil {
		f.log.Warn("list the notes of Lost and Found to drop", "err", err)
		return
	}
	for _, ino := range inos {
		f.forgetUnused(ino)
	}
}

func (f *FS) forgetUnused(ino meta.Ino) {
	if f.handles.opened(ino) {
		return
	}
	if f.queue.holding(ino) {
		return
	}
	it, err := f.store.ByIno(ino)
	if err != nil || it.Standing() != meta.GoneUnsent {
		return
	}

	gone := false
	err = f.shown.made(func(v meta.Visible) (err error) {
		gone, err = v.Forget(it.Ino)
		return err
	})
	if err != nil {
		f.log.Warn("forget a file that never reached Drive", "id", it.ID, "err", err)
		return
	}
	if !gone {
		return
	}
	f.edits.Drop(it.Ino)
	if err := f.cache.Remove(it.Ino, it.ID); err != nil {
		f.log.Warn("drop the content of a forgotten file", "id", it.ID, "err", err)
	}

	f.dropNotes()
}

func (f *FS) forgetUnsent() error {
	items, err := f.store.Unsent()
	if err != nil {
		return err
	}
	for _, it := range items {
		f.forgetUnused(it.Ino)
	}
	return nil
}

func checkName(name string) error {
	if len(name) > meta.MaxName {

		return syscall.ENAMETOOLONG
	}
	if !utf8.ValidString(name) {

		return syscall.EILSEQ
	}
	return nil
}
