package core

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/cache"
	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

const restoreOtherWhy = "файл не дошёл до Drive из прежнего состояния демона, отложенного как недостоверное, " +
	"а под его путём в Drive лежит другое"

type restoring struct {
	src  string
	it   meta.Item
	size int64
	was  string
	dirs []string
	now  time.Time
}

func (f *FS) restoreFile(old *meta.ReadOnly, from string, it meta.Item) (bool, error) {
	r, found, err := f.openRestoring(old, from, it)
	if !found || err != nil {
		return false, err
	}
	lacks, err := lacksBase(r.src, it, r.size)
	if err != nil {
		return false, err
	}
	if lacks && !it.Lost {

		return true, f.restoreLost(r.src, it, r.dirs, conflicted(it.Name, r.now), r.was,
			"файл не дошёл до Drive из прежнего состояния демона, отложенного как недостоверное; части версии Drive, на "+
				"которой сделана правка, в копии нет, на её месте нули", r.size, r.now)
	}
	if it.Lost {

		return true, f.restoreLost(r.src, it, r.dirs[min(1, len(r.dirs)):], it.Name, r.was,
			"файл лежал в «Lost and Found» прежнего состояния демона, которое отложено как недостоверное", r.size, r.now)
	}
	return f.restoreByPlace(r)
}

func (f *FS) openRestoring(old *meta.ReadOnly, from string, it meta.Item) (restoring, bool, error) {

	if !drive.ValidID(strings.TrimSuffix(it.ID, filepath.Ext(it.ID))) {
		return restoring{}, false, fmt.Errorf("the copy names a file %q, which is no fileId", it.ID)
	}
	src := cache.PathIn(ContentDir(from), it.ID)
	fi, err := os.Lstat(src)
	if errors.Is(err, fs.ErrNotExist) {
		return restoring{}, false, nil
	}
	if err != nil {
		return restoring{}, false, err
	}
	was, err := old.LastPath(it.ID)
	if err != nil {
		return restoring{}, false, err
	}
	return restoring{src: src, it: it, size: fi.Size(), was: was, dirs: pathDirs(was), now: f.clock.Now()}, true, nil
}

func (f *FS) restoreByPlace(r restoring) (bool, error) {
	dir, err := f.restoreDirs(r.dirs)
	if errors.Is(err, syscall.ENOTDIR) {
		return true, f.restoreConflicted(r)
	}
	if err != nil {
		return false, err
	}
	target, err := f.store.Lookup(dir.ID, r.it.Name)
	switch {
	case errors.Is(err, meta.ErrNotFound):
		made, err := f.create(uint64(dir.Ino), r.it.Name, meta.KindFile)
		if err != nil {
			return false, err
		}
		return true, f.takeContent(r.src, made, r.size, r.it.Mtime, r.now)
	case err != nil:
		return false, err
	case target.Kind == meta.KindFile && target.ID == r.it.ID && target.MD5 == r.it.MD5 && !target.ContentLocal():
		return f.restoreOver(r, target)
	}
	return true, f.restoreConflicted(r)
}

func (f *FS) restoreOver(r restoring, target meta.Item) (bool, error) {
	if err := f.cache.Remove(target.Ino, target.ID); err != nil {
		return false, err
	}
	if err := f.store.MarkDirty(target.Ino, 0, false); err != nil {
		return false, err
	}
	return true, f.takeContent(r.src, target, r.size, r.it.Mtime, r.now)
}

func (f *FS) restoreConflicted(r restoring) error {
	return f.restoreLost(r.src, r.it, r.dirs, conflicted(r.it.Name, r.now), r.was, restoreOtherWhy, r.size, r.now)
}
