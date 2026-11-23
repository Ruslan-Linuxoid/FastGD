package core

import (
	"context"
	"fmt"
	"slices"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func (c *conflicts) baseGivenUp(ctx context.Context, ino meta.Ino, cause error) error {
	it, err := c.store.ByIno(ino)
	if err != nil || !it.Dirty || !it.RExists {
		return err
	}
	if lacks, err := c.edits.LacksBase(ctx, c.edits.Local(it)); err != nil || !lacks {
		return err
	}
	was, err := c.store.LastPath(it.ID)
	if err != nil {
		return err
	}
	rf, err := c.remote(ctx, it.ID)
	if err != nil {
		return err
	}
	if moved, err := c.shelveAsIs(it, rf, "", whyBaseGivenUp+cause.Error()); err != nil || !moved {
		return err
	}
	c.log.Error("Drive does not give the parts of its version that a file changed here lacks: the local version is in "+
		"Lost and Found",
		"path", was, "err", cause)
	c.keepUndone(was, cause)
	return nil
}

func (c *conflicts) revert(ctx context.Context, op meta.Op, tries int, cause error) error {

	defer c.edits.Recount()
	it, err := c.store.ByIno(op.Ino)
	if err != nil {
		return err
	}
	was, err := c.store.LastPath(it.ID)
	if err != nil {
		return err
	}

	var waiting []meta.Op
	if it.Kind != meta.KindDir {
		if waiting, err = c.store.Successors(op.ID); err != nil {
			return err
		}
	}
	if err := c.undoChange(ctx, op, it, cause); err != nil {
		return err
	}
	c.log.Error("Drive does not take a change made here: it is undone", "path", was, "tries", tries, "err", cause)
	c.keepUndone(was, cause)

	if it.Kind == meta.KindDir {
		return nil
	}
	return c.undoWaiting(it, waiting)
}

func (c *conflicts) undoChange(ctx context.Context, op meta.Op, it meta.Item, cause error) error {
	var err error
	switch {
	case !it.RExists && it.Kind == meta.KindDir:
		err = c.revertFolder(ctx, op, it)
	case !it.RExists:
		err = c.shelve(ctx, it, nil, "", "Drive не принял создание файла: "+cause.Error())
	case it.Dirty && (!it.MetaDirty || !it.TrashedHere()):

		var rf *drive.File
		if rf, err = c.drive.Get(ctx, it.ID); err == nil {
			err = c.shelve(ctx, it, rf, "", "Drive не принял изменение файла: "+cause.Error())
		}
	default:

		err = c.revertMeta(it)
	}
	return err
}

func (c *conflicts) undoWaiting(it meta.Item, waiting []meta.Op) error {
	var err error
	for _, w := range waiting {
		other, oerr := c.store.ByIno(w.Ino)
		if oerr != nil {
			c.log.Error("read a change that waited for one Drive does not take", "ino", w.Ino, "err", oerr)
			err = oerr
			continue
		}
		if other.Ino == it.Ino || !other.RExists {
			continue
		}
		if rerr := c.revertMeta(other); rerr != nil {
			c.log.Error("undo a change that waited for one Drive does not take", "id", other.ID, "err", rerr)
			err = rerr
		}
	}
	return err
}

func (c *conflicts) revertFolder(ctx context.Context, op meta.Op, dir meta.Item) error {
	paths, err := c.unuploadedPaths()
	if err != nil {
		return err
	}
	waiting, err := c.store.Successors(op.ID)
	if err != nil {
		return err
	}
	folders, err := c.undoFolderWaiting(ctx, dir, waiting, paths)
	if err != nil {
		return err
	}

	folders = append([]meta.Item{dir}, folders...)
	for _, folder := range slices.Backward(folders) {
		if err := c.forgetRevertedFolder(folder); err != nil {
			return err
		}
	}
	return nil
}

func (c *conflicts) undoFolderWaiting(
	ctx context.Context, dir meta.Item, waiting []meta.Op, paths map[meta.Ino]string,
) ([]meta.Item, error) {
	var folders []meta.Item
	for _, w := range waiting {
		it, err := c.store.ByIno(w.Ino)
		switch {
		case err != nil || it.Ino == dir.Ino:
		case it.RExists:

			if err := c.revertMeta(it); err != nil {
				return nil, err
			}
		case it.Kind == meta.KindDir:
			folders = append(folders, it)
		default:
			c.resolveNow(ctx, it.Ino, paths[it.Ino])
		}
	}
	return folders, nil
}

func (c *conflicts) forgetRevertedFolder(folder meta.Item) error {
	if err := c.trashServiceKids(folder); err != nil {
		return err
	}
	return c.shown.local(func(v meta.Visible) error {
		gone, err := v.ForgetEmpty(folder.Ino)
		if err == nil && !gone {
			err = fmt.Errorf("the folder %s still holds items", folder.Name)
		}
		return err
	}, folder)
}

func (c *conflicts) trashServiceKids(folder meta.Item) error {
	kids, err := c.store.Children(folder.ID)
	if err != nil {
		return err
	}
	for _, kid := range kids {
		if !kid.Service || kid.Kind == meta.KindDir {
			continue
		}
		if err := c.shown.local(func(v meta.Visible) error { return v.Trash(kid.Ino) }, kid.Item); err != nil {
			return err
		}
		c.forgetUnused(kid.Ino)
	}
	return nil
}

func (c *conflicts) revertMeta(it meta.Item) error {

	err := c.shown.local(func(v meta.Visible) error { return v.RevertMeta(it) },
		it, meta.Item{Parent: it.RParent, Name: it.RName, Mime: it.Mime})
	if err == nil {

		c.shown.restated(it.Ino)
	}
	return err
}

func (c *conflicts) keepUndone(was string, cause error) {
	if err := c.store.SetUndone(c.clock.Now(), was, cause.Error()); err != nil {
		c.log.Warn("keep the undone change for status", "err", err)
	}
}
