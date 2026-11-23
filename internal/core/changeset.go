package core

import (
	"errors"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

type changeSet struct {
	batch []meta.Change

	touched []meta.Item

	folderMoved bool
	ids         []string

	renamed []meta.Item
	paths   map[meta.Ino]string
}

func (f *FS) takeChange(ch drive.Change, cs *changeSet) error {
	old, err := f.store.ByID(ch.FileID)
	if err != nil && !errors.Is(err, meta.ErrNotFound) {

		return err
	}
	known := err == nil
	cs.ids = append(cs.ids, ch.FileID)
	if known {
		cs.touched = append(cs.touched, old)
		if err := f.noteGoingFolder(ch, old, cs); err != nil {
			return err
		}
	}
	if ch.Removed {
		return f.takeRemoval(ch, old, known, cs)
	}
	return f.takeUpdate(ch, old, known, cs)
}

func (f *FS) noteGoingFolder(ch drive.Change, old meta.Item, cs *changeSet) error {
	if old.Kind != meta.KindDir || !ch.Removed && !ch.File.Trashed || cs.paths != nil {
		return nil
	}
	var err error
	cs.paths, err = f.unuploadedPaths()
	return err
}

func (f *FS) takeRemoval(ch drive.Change, old meta.Item, known bool, cs *changeSet) error {
	cs.folderMoved = cs.folderMoved || known && old.Kind == meta.KindDir
	cs.batch = append(cs.batch, meta.Change{ID: ch.FileID, Removed: true})
	if known {
		return f.removeClean(old)
	}
	return nil
}

func (f *FS) takeUpdate(ch drive.Change, old meta.Item, known bool, cs *changeSet) error {
	it := f.toItem(*ch.File)
	cs.touched = append(cs.touched, it)
	if known && renamedBoth(old, it) {
		cs.renamed = append(cs.renamed, it)
	}
	if it.Kind == meta.KindDir && (!known || old.Parent != it.Parent || old.Name != it.Name ||
		old.Trashed != it.Trashed) {
		cs.folderMoved = true
	}
	cs.batch = append(cs.batch, meta.Change{ID: ch.FileID, Item: it})
	if known && contentChanged(old, it) {
		return f.dropClean(old, it)
	}
	return nil
}
