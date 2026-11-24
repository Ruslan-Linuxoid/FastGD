package fusefs

import (
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/hanwen/go-fuse/v2/fuse"
)

const statBlock = 512

func fillAttr(a core.Attr, out *fuse.Attr) {
	out.Ino = a.Ino

	out.Size = uint64(a.Size)
	out.Blocks = (out.Size + statBlock - 1) / statBlock
	out.Blksize = 4096
	out.Mode = a.Mode

	out.Nlink = 1
	out.Owner = fuse.Owner{Uid: a.UID, Gid: a.GID}
	t := a.Mtime
	if t.IsZero() {

		t = time.Unix(0, 0)
	}
	out.SetTimes(&t, &t, &t)
}

func fillEntry(a core.Attr, out *fuse.EntryOut) {
	out.NodeId = a.Ino
	fillAttr(a, &out.Attr)
	out.SetEntryTimeout(cacheTimeout)
	if a.NameUncached {
		out.SetEntryTimeout(0)
	}
	out.SetAttrTimeout(attrTimeout(a))
}

func attrTimeout(a core.Attr) time.Duration {
	if a.Uncached {
		return 0
	}
	return cacheTimeout
}

func (fs *FS) GetXAttr(cancel <-chan struct{}, h *fuse.InHeader, attr string, dest []byte) (uint32, fuse.Status) {
	ctx, stop := withCancel(cancel)
	defer stop()
	v, err := fs.core.GetXattr(ctx, h.NodeId, attr)
	if err != nil {
		return 0, status(err)
	}
	return fillXattr(dest, []byte(v))
}

func (fs *FS) ListXAttr(_ <-chan struct{}, h *fuse.InHeader, dest []byte) (uint32, fuse.Status) {
	names, err := fs.core.ListXattr(h.NodeId)
	if err != nil {
		return 0, status(err)
	}
	var list []byte
	for _, name := range names {
		list = append(append(list, name...), 0)
	}
	return fillXattr(dest, list)
}

func fillXattr(dest, v []byte) (uint32, fuse.Status) {
	if len(dest) > 0 && len(dest) < len(v) {

		return uint32(len(v)), fuse.ERANGE
	}
	copy(dest, v)

	return uint32(len(v)), fuse.OK
}
