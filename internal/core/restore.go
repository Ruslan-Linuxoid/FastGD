package core

import (
	"errors"
	"os"
	"slices"
	"syscall"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"golang.org/x/sys/unix"
)

func (f *FS) restore() error {
	from, pending, err := f.store.GetState(meta.StateRestoreFrom)
	if err != nil || !pending {
		return err
	}
	old, err := meta.OpenReadOnly(DBPath(from))
	if err != nil {
		f.log.Error("the database of the copy cannot be read: nothing is restored from it", "copy", from, "err", err)
		return f.store.DeleteState(meta.StateRestoreFrom)
	}
	defer old.Close()
	files, err := old.Unsent()
	if err != nil {
		f.log.Error("the copy does not tell what had not reached Drive: nothing is restored from it", "copy", from, "err",
			err)
		return f.store.DeleteState(meta.StateRestoreFrom)
	}
	restored, left := 0, 0
	for _, it := range files {
		done, err := f.restoreFile(old, from, it)
		switch {
		case err != nil:
			left++
			f.log.Error("a file that had not reached Drive is not restored and stays in the copy", "copy", from, "id", it.ID,
				"name", it.Name, "err", err)
		case done:
			restored++
		}
	}
	f.log.Warn("restored from the copy of the state what had not reached Drive", "copy", from, "files", restored,
		"left_in_the_copy", left)
	return f.store.DeleteState(meta.StateRestoreFrom)
}

func lacksBase(src string, it meta.Item, size int64) (bool, error) {
	base := min(it.Base, size)
	if base == 0 {
		return false, nil
	}
	if it.BaseTail {
		return true, nil
	}

	fd, err := os.Open(src)
	if err != nil {
		return false, err
	}
	defer fd.Close()
	hole, err := unix.Seek(int(fd.Fd()), 0, unix.SEEK_HOLE)
	if err != nil {
		return false, err
	}
	return hole < base, nil
}

func (f *FS) restoreDirs(dirs []string) (meta.Item, error) {
	from := meta.TopID
	if len(dirs) == 0 || !slices.Contains([]string{meta.MyDriveName, meta.SharedWithMeName, meta.SharedDrivesName},
		dirs[0]) {
		root, _, err := f.store.GetState(meta.StateRootID)
		if err != nil {
			return meta.Item{}, err
		}
		from = root
	}
	dir, err := f.store.ByID(from)
	if err != nil {
		return meta.Item{}, err
	}
	for _, name := range dirs {
		next, err := f.store.Lookup(dir.ID, name)
		if errors.Is(err, meta.ErrNotFound) {
			next, err = f.create(uint64(dir.Ino), name, meta.KindDir)
		}
		if err != nil {
			return meta.Item{}, err
		}
		if next.Kind != meta.KindDir {
			return meta.Item{}, syscall.ENOTDIR
		}
		dir = next
	}
	return dir, nil
}

func (f *FS) takeContent(src string, it meta.Item, size int64, mtime, now time.Time) error {
	if err := f.cache.CreateLocal(it.ID); err != nil {
		return err
	}
	if err := os.Rename(src, f.cache.Path(it.ID)); err != nil {
		return err
	}
	return f.store.SetLocal(it.Ino, size, mtime, it.Gen+1, now)
}

func (f *FS) restoreLost(
	src string, it meta.Item, dirs []string, name, was, why string, size int64, now time.Time,
) error {

	id := meta.LocalKeyFor(src)
	made, err := f.store.Knows(id)
	if err != nil {
		return err
	}
	if made {
		f.dropFromCopy(src, was)
		return nil
	}
	if err := f.cache.CreateLocal(id); err != nil {
		return err
	}
	note, err := f.conflicts.writeNote(name, was, why, now)
	if err != nil {
		f.log.Warn("write the note beside a file in Lost and Found", "path", was, "err", err)
	}

	dst := f.cache.Path(id)
	if err := f.linkLost(src, dst); err != nil {
		return err
	}
	err = f.shown.made(func(v meta.Visible) error {
		return v.AddLost(id, dirs, name, it.Mime, size, it.Mtime, note, now)
	})
	if err != nil {
		if rerr := os.Remove(dst); rerr != nil {
			f.log.Warn("drop the name in the cache of a file that stays in the copy", "path", was, "err", rerr)
		}
		return err
	}
	if err := f.conflicts.killed(stepRestoreRow); err != nil {
		return err
	}
	f.dropFromCopy(src, was)
	return nil
}

func (f *FS) dropFromCopy(src, was string) {
	if err := os.Remove(src); err != nil {
		f.log.Warn("the copy keeps a file that is in Lost and Found too", "path", was, "err", err)
	}
}

func (f *FS) linkLost(src, dst string) error {
	if err := os.Remove(dst); err != nil {
		return err
	}
	if err := os.Link(src, dst); err != nil {
		return err
	}
	return f.conflicts.killed(stepRestoreLink)
}
