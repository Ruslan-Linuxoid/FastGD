package core

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func (f *FS) SetNotifier(n Notifier) { f.shown.set(n) }

func (f *FS) PollChanges(ctx context.Context) error {
	err := f.pollChanges(ctx)
	switch {
	case err == nil:
		f.signedIn()
	case drive.IsLoginExpired(err):
		f.signedOut(err)
	case errors.Is(err, meta.ErrIDReused) || errors.Is(err, ErrUntrusted):
		f.distrust(err.Error())
	}
	return err
}

func (f *FS) pollChanges(ctx context.Context) error {
	f.pollMu.Lock()
	defer f.pollMu.Unlock()
	token, ok, err := f.store.GetState(meta.StateChangesToken)
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("poll changes: no Changes token in the database")
	}
	changes, next, err := f.opt.Drive.Changes(ctx, token)
	if err != nil {
		return err
	}
	if why, ahead := aheadOfDrive(token, next); ahead {
		return fmt.Errorf("%w: %s", ErrUntrusted, why)
	}
	if len(changes) == 0 && next == token {
		return f.notePoll()
	}
	if err := f.applyChanges(ctx, changes, next); err != nil {
		return err
	}
	return f.notePoll()
}

func (f *FS) applyChanges(ctx context.Context, changes []drive.Change, next string) error {
	rootID, _, err := f.store.GetState(meta.StateRootID)
	if err != nil {
		return err
	}

	release := sync.OnceFunc(f.edits.HoldClean())
	defer release()
	cs := changeSet{
		batch: make([]meta.Change, 0, len(changes)),
		ids:   make([]string, 0, len(changes)),
	}
	for _, ch := range changes {
		if ch.FileID == rootID {
			continue
		}
		if err := f.takeChange(ch, &cs); err != nil {
			return err
		}
	}
	if f.opt.PollFault != nil {
		if err := f.opt.PollFault(); err != nil {
			return err
		}
	}
	applied, err := f.shown.remote(cs.touched, cs.ids, cs.folderMoved, func(v meta.Visible) ([]meta.Applied, error) {
		applied, err := v.ApplyChanges(cs.batch, next)
		release()
		return applied, err
	})
	if err != nil {
		return err
	}
	if err := f.conflicts.resolveChanged(ctx, applied, cs.renamed, cs.paths); err != nil {
		return err
	}

	f.edits.Recount()
	f.log.Debug("applied changes", "count", len(applied))
	return nil
}

func (f *FS) unuploadedPaths() (map[meta.Ino]string, error) {
	items, err := f.store.Unuploaded()
	if err != nil {
		return nil, err
	}
	paths := make(map[meta.Ino]string, len(items))
	for _, it := range items {
		if paths[it.Ino], err = f.store.LastPath(it.ID); err != nil {
			return nil, err
		}
	}
	return paths, nil
}

func (c *conflicts) stranded(ctx context.Context, paths map[meta.Ino]string) {
	items, err := c.store.Unuploaded()
	if err != nil {
		c.log.Warn("list what did not reach Drive", "err", err)
		return
	}
	var dirs []meta.Item
	for _, it := range items {
		if !c.strandedItem(it) {
			continue
		}
		if it.Kind == meta.KindDir {
			dirs = append(dirs, it)
			continue
		}
		c.moveStranded(ctx, it, paths)
	}
	c.forgetStrandedDirs(dirs)
}
