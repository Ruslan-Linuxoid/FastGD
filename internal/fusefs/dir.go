package fusefs

import (
	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/hanwen/go-fuse/v2/fuse"
)

func (fs *FS) OpenDir(_ <-chan struct{}, in *fuse.OpenIn, out *fuse.OpenOut) fuse.Status {
	entries, err := fs.core.ReadDir(in.NodeId)
	if err != nil {
		return status(err)
	}
	fs.mu.Lock()
	fs.nextDir++
	out.Fh = fs.nextDir
	fs.dirs[out.Fh] = entries
	fs.mu.Unlock()
	return fuse.OK
}

func (fs *FS) ReleaseDir(in *fuse.ReleaseIn) {
	fs.mu.Lock()
	delete(fs.dirs, in.Fh)
	fs.mu.Unlock()
}

func (fs *FS) ReadDir(_ <-chan struct{}, in *fuse.ReadIn, out *fuse.DirEntryList) fuse.Status {
	return fs.readDir(in, out, false)
}

func (fs *FS) ReadDirPlus(_ <-chan struct{}, in *fuse.ReadIn, out *fuse.DirEntryList) fuse.Status {
	return fs.readDir(in, out, true)
}

func (fs *FS) readDir(in *fuse.ReadIn, out *fuse.DirEntryList, plus bool) fuse.Status {
	fs.mu.Lock()
	entries, ok := fs.dirs[in.Fh]
	fs.mu.Unlock()
	if !ok {
		return fuse.EBADF
	}

	for i := int(in.Offset); i < len(entries)+2; i++ {

		de := fuse.DirEntry{Mode: fuse.S_IFDIR, Off: uint64(i + 1)}
		var a core.Attr
		switch i {
		case 0:
			de.Name = "."
		case 1:
			de.Name = ".."
		default:
			a = entries[i-2].Attr
			de.Name, de.Mode, de.Ino = entries[i-2].Name, a.Mode, a.Ino
		}
		if !plus {
			if !out.AddDirEntry(de) {
				break
			}
			continue
		}
		eo := out.AddDirLookupEntry(de)
		if eo == nil {
			break
		}

		if i >= 2 {
			fillEntry(a, eo)
		}
	}
	return fuse.OK
}
