package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func (f *FS) reconcile(ctx context.Context, it meta.Item) error {
	it, err := f.recorded(it)
	if err != nil {
		return err
	}
	var stop bool
	if it, stop, err = f.sendKeptFirst(it); err != nil || stop {
		return err
	}
	done, it, err := f.conflicts.resolve(ctx, it)
	if err != nil || done {
		return err
	}
	if handled, err := f.reconcileStanding(ctx, it); handled {
		return err
	}
	return f.reconcileContent(ctx, it)
}

func (f *FS) sendKeptFirst(it meta.Item) (meta.Item, bool, error) {
	if it.Kind != meta.KindFile || !it.Dirty || it.Trashed {
		return it, false, nil
	}

	it, err := f.edits.TakenUp(it)
	if err != nil {
		return it, false, err
	}
	kept, err := f.keepSent(it)
	if err != nil || !kept {
		return it, false, err
	}
	it, err = f.store.ByIno(it.Ino)
	return it, err != nil || !it.AheadOfDrive(), err
}

func (f *FS) reconcileStanding(ctx context.Context, it meta.Item) (bool, error) {
	standing := it.Standing()
	switch {
	case standing == meta.Broken:
		return true, fmt.Errorf("the item %s has flags that contradict each other: %+v", it.ID, it)
	case standing == meta.GoneUnsent || standing == meta.GoneInDoubt:
		return true, f.trashUnconfirmed(ctx, it)
	case it.Trashed:

		return true, f.sendMeta(ctx, it)
	case it.Kind == meta.KindDir && standing == meta.ToCreate:
		return true, f.createFolder(ctx, it)
	case it.Kind == meta.KindDir:
		return true, f.sendMeta(ctx, it)
	}
	return false, nil
}

func (f *FS) reconcileContent(ctx context.Context, it meta.Item) error {
	if it.ContentLocal() {
		if err := f.uploadFile(ctx, it); err != nil {
			return err
		}
		fresh, err := f.store.ByIno(it.Ino)
		if err != nil {
			return err
		}
		it = fresh
	}
	if it.MetaWaits() {
		return f.sendMeta(ctx, it)
	}
	return nil
}

func (f *FS) recorded(it meta.Item) (meta.Item, error) {
	st := f.edits.StateIf(it.Ino)
	if st == nil {
		return it, nil
	}
	unrecorded := st.Pending()
	if unrecorded == nil {
		return it, nil
	}
	if err := f.record(st, *unrecorded); err != nil {
		return it, err
	}
	return f.store.ByIno(it.Ino)
}

func (f *FS) trashUnconfirmed(ctx context.Context, it meta.Item) error {
	rf, sent, err := f.existing(ctx, it.ID)
	if errors.Is(err, drive.ErrNotFound) {

		return f.store.ClearCreateTried(it.Ino)
	}
	if err != nil {
		return err
	}

	sent.Trashed = false
	if err := f.store.MarkCreated(it.Ino, remoteOf(rf), sent); err != nil {
		return err
	}
	fresh, err := f.store.ByIno(it.Ino)
	if err != nil {
		return err
	}
	return f.sendMeta(ctx, fresh)
}

func (f *FS) createFolder(ctx context.Context, it meta.Item) error {
	sent := sentByCreate(it, it.Mtime)
	rf, err := f.opt.Drive.CreateFolder(ctx, it.ID, sent.Name, sent.Parent, sent.Mtime)
	if errors.Is(err, drive.ErrExists) {

		if rf, sent, err = f.existing(ctx, it.ID); err == nil && rf.MimeType != drive.MimeFolder {

			err = fmt.Errorf("the fileId of the folder %q made here holds an item of the type %s in Drive", it.Name, rf.MimeType)
		}
	}
	if err != nil {
		return err
	}
	return f.store.MarkCreated(it.Ino, remoteOf(rf), sent)
}

func newMime(it meta.Item) string { return drive.MimeFor(it.Name) }

func (f *FS) existing(ctx context.Context, id string) (*drive.File, meta.Sent, error) {
	rf, err := f.opt.Drive.Get(ctx, id)
	if err != nil {
		return nil, meta.Sent{}, err
	}
	return rf, sentOf(rf), nil
}

func (f *FS) sendMeta(ctx context.Context, it meta.Item) error {
	it, err := f.followDriveParent(it)
	if err != nil {
		return err
	}
	update := drive.MetaUpdate{
		Name:      it.Name,
		OldParent: it.RParent,
		NewParent: it.Parent,
		Trashed:   it.Trashed,
		Modified:  it.Mtime,
	}
	if meta.IsVirtual(it.Parent) || meta.IsVirtual(it.RParent) {

		update.OldParent, update.NewParent = "", ""
	}
	sent := sentByUpdate(update)
	rf, err := f.opt.Drive.UpdateMeta(ctx, it.ID, update)
	if err != nil {
		return err
	}
	if err := f.store.MarkMetaSent(it.Ino, remoteOf(rf), sent); err != nil {
		return err
	}
	if sent.Trashed {
		f.dropTrashedContent(it)
	}
	return nil
}

func (f *FS) followDriveParent(it meta.Item) (meta.Item, error) {
	if !it.Trashed || it.Parent == it.RParent {
		return it, nil
	}

	if err := f.shown.made(func(v meta.Visible) error { return v.Rename(it, it.RParent, it.Name, nil) }); err != nil {
		return it, err
	}
	it.Parent = it.RParent
	return it, nil
}

func (f *FS) dropTrashedContent(it meta.Item) {
	if !it.ContentLocal() || f.handles.opened(it.Ino) {
		return
	}

	now, err := f.store.ByIno(it.Ino)
	if err != nil {
		f.log.Warn("read an item that went to the trash: its content stays on the disk", "id", it.ID, "err", err)
		return
	}
	if now.ContentLocal() {
		return
	}
	if err := f.cache.Remove(it.Ino, it.ID); err != nil {
		f.log.Warn("drop the content of an edited file that went to the trash", "id", it.ID, "err", err)
		return
	}
	f.edits.Drop(it.Ino)
}
