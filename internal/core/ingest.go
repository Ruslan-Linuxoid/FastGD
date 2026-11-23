package core

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func (f *FS) initialLoad(ctx context.Context) error {
	start := f.clock.Now()
	rootID, err := f.startLoad(ctx, start)
	if err != nil {
		return err
	}
	n := 0
	err = f.opt.Drive.ListAll(ctx, func(files []drive.File) error {
		items := make([]meta.Item, 0, len(files))
		for _, df := range files {
			if df.ID == rootID {
				continue
			}
			items = append(items, f.toItem(df))
		}
		n += len(items)
		return f.store.PutItems(items)
	})
	if err != nil {
		return fmt.Errorf("initial load: %w", err)
	}
	if err := f.PollChanges(ctx); err != nil {
		return fmt.Errorf("initial load: %w", err)
	}
	if err := f.fillIDs(ctx); err != nil {
		return fmt.Errorf("initial load: %w", err)
	}
	if err := f.store.SetState(meta.StateFields, f.opt.FieldsMark); err != nil {
		return err
	}
	if err := f.store.SetState(meta.StateLoaded, "1"); err != nil {
		return err
	}
	f.log.Info("initial load done", "items", n, "took", f.clock.Now().Sub(start))
	return nil
}

func (f *FS) startLoad(ctx context.Context, start time.Time) (string, error) {
	if err := f.store.ResetItems(); err != nil {
		return "", err
	}
	rootID, err := f.opt.Drive.RootID(ctx)
	if err != nil {
		return "", err
	}
	token, err := f.opt.Drive.StartPageToken(ctx)
	if err != nil {
		return "", err
	}
	if err := f.store.SetState(meta.StateRootID, rootID); err != nil {
		return "", err
	}
	if err := f.store.SetState(meta.StateChangesToken, token); err != nil {
		return "", err
	}
	return rootID, f.store.SetRoot(rootID, start)
}

func (f *FS) rereadTree(ctx context.Context) error {
	have, _, err := f.store.GetState(meta.StateFields)
	if err != nil || have == f.opt.FieldsMark {
		return err
	}
	if err := f.reread(ctx); err != nil {
		if errors.Is(err, meta.ErrIDReused) {
			f.distrust(err.Error())
			return fmt.Errorf("%w: %w", ErrUntrusted, err)
		}
		if ctx.Err() != nil {
			return err
		}
		f.log.Warn("the tree was read with another set of fields, and is not read anew now: the next start tries again",
			"err", err)
		return nil
	}
	f.log.Info("the tree was read anew from Drive: the set of fields the client asks for is another")
	return f.store.SetState(meta.StateFields, f.opt.FieldsMark)
}

func (f *FS) reread(ctx context.Context) error {
	f.pollMu.Lock()
	defer f.pollMu.Unlock()
	rootID, _, err := f.store.GetState(meta.StateRootID)
	if err != nil {
		return err
	}
	old, _, err := f.store.GetState(meta.StateChangesToken)
	if err != nil {
		return err
	}
	token, err := f.opt.Drive.StartPageToken(ctx)
	if err != nil {
		return err
	}
	listed := map[string]bool{rootID: true}

	err = f.opt.Drive.ListAll(ctx, func(files []drive.File) error {
		changes := make([]drive.Change, 0, len(files))
		for i := range files {
			listed[files[i].ID] = true
			changes = append(changes, drive.Change{FileID: files[i].ID, File: &files[i]})
		}
		return f.applyChanges(ctx, changes, old)
	})
	if err != nil {
		return err
	}
	ids, err := f.store.InDrive()
	if err != nil {
		return err
	}
	var gone []drive.Change
	for _, id := range ids {
		if !listed[id] {
			gone = append(gone, drive.Change{FileID: id, Removed: true})
		}
	}
	return f.applyChanges(ctx, gone, token)
}

func (f *FS) toItem(df drive.File) meta.Item {
	it := meta.Item{
		ID:             df.ID,
		Kind:           meta.KindOf(df.MimeType),
		Mime:           df.MimeType,
		Name:           df.Name,
		Trashed:        df.Trashed,
		Size:           df.Size,
		MD5:            df.MD5,
		Rev:            df.HeadRevision,
		Version:        df.Version,
		Mtime:          df.Modified,
		Ctime:          df.Created,
		ShortcutTarget: df.ShortcutTarget,
		Denied:         deniedOf(df.Can),
	}
	if len(df.Parents) > 0 {
		it.Parent = df.Parents[0]
	}
	if len(df.Parents) > 1 {

		f.log.Warn("item has several parents, using the first", "id", df.ID, "parents", df.Parents)
	}
	return it
}

func deniedOf(can drive.Rights) meta.Denied {
	var d meta.Denied
	for _, r := range []struct {
		ok   bool
		deny meta.Denied
	}{
		{can.ModifyContent, meta.DenyModify},
		{can.Trash, meta.DenyTrash},
		{can.Rename, meta.DenyRename},
		{can.Move, meta.DenyMove},
		{can.AddChildren, meta.DenyAddChildren},
		{can.RemoveChildren, meta.DenyRemoveChildren},
		{can.Download, meta.DenyDownload},
	} {
		if !r.ok {
			d |= r.deny
		}
	}
	return d
}

func (f *FS) loadRights(ctx context.Context) {
	if _, pending, err := f.store.GetState(meta.StateRightsUnknown); err != nil || !pending {
		return
	}
	err := f.opt.Drive.ListAll(ctx, func(files []drive.File) error {
		denied := make(map[string]meta.Denied, len(files))
		for _, df := range files {
			denied[df.ID] = deniedOf(df.Can)
		}
		return f.shown.attrs(func(v meta.Visible) ([]meta.Ino, error) { return v.SetDenied(denied) })
	})
	if err == nil {
		err = f.store.DeleteState(meta.StateRightsUnknown)
	}
	if err != nil {
		if f.tells(ctx, err) {
			f.log.Warn("read the rights of the items from Drive", "err", err)
		}
		return
	}
	f.log.Info("read the rights of the items from Drive")
}
