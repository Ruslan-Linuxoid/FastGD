package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func (f *FS) wantIDs() {
	select {
	case f.idsWanted <- struct{}{}:
	default:
	}
}

func (f *FS) fillIDs(ctx context.Context) error {
	n, err := f.store.PoolSize()
	if err != nil || n >= idLow {
		return err
	}
	ids, err := f.opt.Drive.GenerateIDs(ctx, idFill)
	if err != nil {
		return err
	}
	return f.store.AddIDs(ids)
}

func (f *FS) giveID(ctx context.Context, it meta.Item) (meta.Item, error) {
	if meta.IsLocalKey(it.Parent) {
		return it, fmt.Errorf("the folder of %s has no fileId yet", it.Name)
	}
	id, err := f.takeID(ctx)
	if err != nil {
		return it, err
	}
	f.wantIDs()
	if it.Kind == meta.KindFile {

		st := f.edits.Lock(it)
		defer st.Unlock()
		if err := f.cache.LinkLocal(it.ID, id); err != nil {
			return it, err
		}
	}

	if err := f.shown.made(func(v meta.Visible) error { return v.Rebind(it, id) }); err != nil {
		f.unlinkSecondName(it, id)
		return it, err
	}
	f.unlinkOldName(it, id)
	return f.store.ByIno(it.Ino)
}

func (f *FS) takeID(ctx context.Context) (string, error) {
	id, err := f.store.TakeID()
	if errors.Is(err, meta.ErrNoIDs) {

		if err = f.fillIDs(ctx); err == nil {
			id, err = f.store.TakeID()
		}
	}
	return id, err
}

func (f *FS) unlinkSecondName(it meta.Item, id string) {
	if it.Kind != meta.KindFile {
		return
	}
	if err := f.cache.UnlinkLocal(id); err != nil {
		f.log.Warn("drop the second name of a file's content", "id", it.ID, "err", err)
	}
}

func (f *FS) unlinkOldName(it meta.Item, id string) {
	if it.Kind != meta.KindFile {
		return
	}
	err := f.cache.UnlinkLocal(it.ID)
	if err == nil {
		err = f.store.Shelf().Finished(id)
	}
	if err != nil {

		f.log.Error("drop the old name of a file's content", "id", it.ID, "err", err)
	}
}
