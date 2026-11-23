package core

import (
	"errors"
	"syscall"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

type Attr struct {
	Ino   uint64
	Mode  uint32
	Size  int64
	Mtime time.Time
	UID   uint32
	GID   uint32

	Uncached bool
}

type DirEntry struct {
	Name string
	Attr Attr
}

func (f *FS) attr(it meta.Item) Attr {
	link := ""
	if it.Kind == meta.KindShortcut {
		link, _ = f.store.Link(it)
	}
	return f.attrOf(it, link)
}

const writeBits = 0o222

func (f *FS) attrOf(it meta.Item, link string) Attr {
	a := Attr{Ino: uint64(it.Ino), Size: it.Size, Mtime: it.Mtime, UID: f.opt.UID, GID: f.opt.GID}
	if size, mtime, ok := f.edits.Seen(it.Ino); ok {
		a.Size, a.Mtime = size, mtime
	}

	switch it.Kind {
	case meta.KindDir:
		a.Mode = syscall.S_IFDIR | f.opt.DirMode
		a.Size = 0
	case meta.KindShortcut:
		a.Mode = syscall.S_IFLNK | 0o777

		a.Uncached = link == "" || len(link) >= maxLink
		a.Size = int64(len(link))
	case meta.KindDoc:

		a.Mode = syscall.S_IFREG | f.opt.FileMode&^writeBits
		a.Size = int64(len(linkPage(it)))
	default:
		a.Mode = syscall.S_IFREG | f.opt.FileMode
		if it.Denied.Has(meta.DenyModify) {

			a.Mode = syscall.S_IFREG | f.opt.FileMode&^writeBits
		}
	}
	return a
}

func errno(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, meta.ErrNotFound), errors.Is(err, meta.ErrNotShown):
		return syscall.ENOENT
	case errors.Is(err, meta.ErrLostOnly):
		return syscall.EPERM
	}
	return syscall.EIO
}

func (f *FS) item(ino uint64) (meta.Item, error) {
	it, err := f.store.ByIno(meta.Ino(ino))
	if err != nil {
		if !errors.Is(err, meta.ErrNotFound) {
			f.log.Error("read item", "ino", ino, "err", err)
		}
		return meta.Item{}, errno(err)
	}
	return it, nil
}

func (f *FS) GetAttr(ino uint64) (Attr, error) {
	it, err := f.item(ino)
	if err != nil {
		return Attr{}, err
	}
	return f.attr(it), nil
}

func (f *FS) Lookup(parent uint64, name string) (Attr, error) {
	_, it, err := f.child(parent, name)
	if err != nil {
		return Attr{}, err
	}
	return f.attr(it), nil
}

func (f *FS) ReadDir(ino uint64) ([]DirEntry, error) {
	p, err := f.item(ino)
	if err != nil {
		return nil, err
	}
	if p.Kind != meta.KindDir {
		return nil, syscall.ENOTDIR
	}
	entries, err := f.store.Children(p.ID)
	if err != nil {
		return nil, f.eio("readdir", err, "parent", p.ID)
	}
	out := make([]DirEntry, len(entries))
	for i, e := range entries {
		out[i] = DirEntry{Name: e.Display, Attr: f.attrOf(e.Item, e.Link)}
	}
	return out, nil
}
