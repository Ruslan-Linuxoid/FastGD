package fusefs

import (
	"context"
	"errors"
	"sync"
	"syscall"
	"time"

	"github.com/hanwen/go-fuse/v2/fuse"

	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
)

const cacheTimeout = time.Hour

type FS struct {
	fuse.RawFileSystem

	core *core.FS

	mu      sync.Mutex
	nextDir uint64

	dirs map[uint64][]core.DirEntry
}

const fsName = "fastgd"

func Mount(dir string, c *core.FS) (*fuse.Server, error) {
	fsys := &FS{RawFileSystem: fuse.NewDefaultRawFileSystem(), core: c, dirs: map[uint64][]core.DirEntry{}}
	srv, err := fuse.NewServer(fsys, dir, &fuse.MountOptions{
		FsName: fsName,
		Name:   fsName,

		AllowOther: true,
		Options:    []string{"default_permissions"},

		MaxReadAhead: 4096,
	})
	if err != nil {
		return nil, err
	}
	go srv.Serve()
	if err := srv.WaitMount(); err != nil {
		_ = srv.Unmount()
		return nil, err
	}
	return srv, nil
}

func (fs *FS) String() string { return fsName }

func status(err error) fuse.Status {
	if err == nil {
		return fuse.OK
	}
	if errno, ok := errors.AsType[syscall.Errno](err); ok {
		return fuse.Status(errno)
	}
	return fuse.EIO
}

func withCancel(cancel <-chan struct{}) (context.Context, context.CancelFunc) {
	ctx, stop := context.WithCancel(context.Background())
	if cancel != nil {
		go func() {
			select {
			case <-cancel:
				stop()
			case <-ctx.Done():
			}
		}()
	}
	return ctx, stop
}

func (fs *FS) Lookup(_ <-chan struct{}, h *fuse.InHeader, name string, out *fuse.EntryOut) fuse.Status {
	a, err := fs.core.Lookup(h.NodeId, name)
	if err != nil {
		return status(err)
	}
	fillEntry(a, out)
	return fuse.OK
}

func (fs *FS) GetAttr(_ <-chan struct{}, in *fuse.GetAttrIn, out *fuse.AttrOut) fuse.Status {
	a, err := fs.core.GetAttr(in.NodeId)
	if err != nil {
		return status(err)
	}
	fillAttr(a, &out.Attr)
	out.SetTimeout(attrTimeout(a))
	return fuse.OK
}

func (fs *FS) Readlink(_ <-chan struct{}, h *fuse.InHeader) ([]byte, fuse.Status) {
	link, err := fs.core.Readlink(h.NodeId)
	if err != nil {
		return nil, status(err)
	}
	return []byte(link), fuse.OK
}

func (fs *FS) Open(cancel <-chan struct{}, in *fuse.OpenIn, out *fuse.OpenOut) fuse.Status {
	ctx, stop := withCancel(cancel)
	defer stop()
	fh, err := fs.core.Open(ctx, in.NodeId, in.Flags)
	if err != nil {
		return status(err)
	}
	out.Fh = fh

	out.OpenFlags = fuse.FOPEN_KEEP_CACHE
	return fuse.OK
}

func (fs *FS) Read(cancel <-chan struct{}, in *fuse.ReadIn, buf []byte) (fuse.ReadResult, fuse.Status) {
	ctx, stop := withCancel(cancel)
	defer stop()
	buf = buf[:min(len(buf), int(in.Size))]

	n, err := fs.core.Read(ctx, in.Fh, int64(in.Offset), buf)
	if err != nil {
		return nil, status(err)
	}
	return fuse.ReadResultData(buf[:n]), fuse.OK
}

func (fs *FS) Release(_ <-chan struct{}, in *fuse.ReleaseIn) { fs.core.Release(in.Fh) }

func (fs *FS) StatFs(_ <-chan struct{}, _ *fuse.InHeader, out *fuse.StatfsOut) fuse.Status {
	const unit = 4096
	total, free := fs.core.StatFs()
	out.Bsize = unit
	out.Frsize = unit
	out.NameLen = core.MaxName

	out.Blocks = uint64(total / unit)

	out.Bfree = uint64(free / unit)
	out.Bavail = out.Bfree
	return fuse.OK
}

func Notifier(srv *fuse.Server) core.Notifier { return notifier{srv} }

type notifier struct{ srv *fuse.Server }

func (n notifier) Entry(parent uint64, name string) { n.srv.EntryNotify(parent, name) }

func (n notifier) Attrs(ino uint64) { n.srv.InodeNotify(ino, -1, 0) }

func (n notifier) Content(ino uint64) { n.srv.InodeNotify(ino, 0, 0) }

func (fs *FS) Create(_ <-chan struct{}, in *fuse.CreateIn, name string, out *fuse.CreateOut) fuse.Status {
	a, fh, err := fs.core.Create(in.NodeId, name, in.Flags)
	if err != nil {
		return status(err)
	}
	fillEntry(a, &out.EntryOut)
	out.Fh = fh
	out.OpenFlags = fuse.FOPEN_KEEP_CACHE
	return fuse.OK
}

func (fs *FS) Mkdir(_ <-chan struct{}, in *fuse.MkdirIn, name string, out *fuse.EntryOut) fuse.Status {
	a, err := fs.core.Mkdir(in.NodeId, name)
	if err != nil {
		return status(err)
	}
	fillEntry(a, out)
	return fuse.OK
}

func (fs *FS) Write(cancel <-chan struct{}, in *fuse.WriteIn, data []byte) (uint32, fuse.Status) {
	ctx, stop := withCancel(cancel)
	defer stop()

	n, err := fs.core.Write(ctx, in.Fh, int64(in.Offset), data)
	if err != nil {
		return 0, status(err)
	}

	return uint32(n), fuse.OK
}

func (fs *FS) Fsync(_ <-chan struct{}, in *fuse.FsyncIn) fuse.Status {
	return status(fs.core.Fsync(in.Fh))
}

func (fs *FS) Flush(_ <-chan struct{}, _ *fuse.FlushIn) fuse.Status { return fuse.OK }

func (fs *FS) SetAttr(cancel <-chan struct{}, in *fuse.SetAttrIn, out *fuse.AttrOut) fuse.Status {
	if size, ok := in.GetSize(); ok {
		ctx, stop := withCancel(cancel)
		defer stop()

		if err := fs.core.Truncate(ctx, in.NodeId, int64(size)); err != nil {
			return status(err)
		}
	}
	if mtime, ok := in.GetMTime(); ok {
		if err := fs.core.SetMtime(in.NodeId, mtime); err != nil {
			return status(err)
		}
	}
	a, err := fs.core.GetAttr(in.NodeId)
	if err != nil {
		return status(err)
	}
	fillAttr(a, &out.Attr)
	out.SetTimeout(cacheTimeout)
	return fuse.OK
}

func (fs *FS) Unlink(_ <-chan struct{}, h *fuse.InHeader, name string) fuse.Status {
	return status(fs.core.Unlink(h.NodeId, name))
}

func (fs *FS) Rmdir(_ <-chan struct{}, h *fuse.InHeader, name string) fuse.Status {
	return status(fs.core.Rmdir(h.NodeId, name))
}

func (fs *FS) Rename(_ <-chan struct{}, in *fuse.RenameIn, oldName, newName string) fuse.Status {
	return status(fs.core.Rename(in.NodeId, oldName, in.Newdir, newName, in.Flags))
}

func (fs *FS) Symlink(_ <-chan struct{}, _ *fuse.InHeader, _, _ string, _ *fuse.EntryOut) fuse.Status {
	return fuse.EPERM
}

func (fs *FS) Link(_ <-chan struct{}, _ *fuse.LinkIn, _ string, _ *fuse.EntryOut) fuse.Status {
	return fuse.EPERM
}
