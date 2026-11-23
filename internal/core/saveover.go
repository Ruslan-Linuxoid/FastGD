package core

import (
	"context"

	"github.com/Ruslan-Linuxoid/FastGD/internal/edited"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func (f *FS) swapInto(v meta.Visible, src, target meta.Item, parent, name string) (bool, error) {
	if !swappable(src, target) {
		return false, nil
	}

	if _, err := f.edits.TakenUp(src); err != nil {
		return false, nil
	}

	if !f.queue.hold(src.Ino, target.Ino) {
		return false, nil
	}
	defer f.queue.release(src.Ino, target.Ino)
	ctx := context.Background()
	if !f.wholeForSwap(ctx, src) {
		return false, nil
	}
	sw := swapping{v: v, st: f.edits.Lock(src), src: src, parent: parent, name: name}
	defer sw.st.Unlock()
	sw.ts = f.edits.Lock(target)
	defer sw.ts.Unlock()
	sw.target = target

	defer f.edits.HoldClean()()
	return f.swapLocked(ctx, sw)
}

type swapping struct {
	v            meta.Visible
	st, ts       *edited.State
	src, target  meta.Item
	parent, name string
}

func swappable(src, target meta.Item) bool {
	return src.Kind == meta.KindFile && target.Kind == meta.KindFile && target.RExists &&
		!target.LocalOnly() && !target.Denied.Has(meta.DenyModify)
}

func (f *FS) wholeForSwap(ctx context.Context, src meta.Item) bool {
	whole, err := f.wholeHere(ctx, src)
	if err != nil {

		f.log.Warn("the content of a file renamed over another cannot be looked at: the files are renamed, not swapped",
			"id", src.ID, "name", src.Name, "err", err)
	}
	return err == nil && whole
}

func (f *FS) swapLocked(ctx context.Context, sw swapping) (bool, error) {
	var err error
	if sw.src, err = f.store.ByIno(sw.src.Ino); err != nil {
		return false, err
	}
	if sw.target, err = f.store.ByIno(sw.target.Ino); err != nil {
		return false, err
	}
	if f.swapBlocked(sw) {
		return false, nil
	}
	ok, err := f.dropSwapBase(ctx, &sw.src)
	if !ok {
		return false, err
	}
	recorded, err := f.swapRows(sw)
	if !recorded {
		return false, err
	}

	if now, ierr := f.store.ByIno(sw.src.Ino); ierr == nil {
		sw.src = now
	} else {
		f.log.Error("read the file that took the fileId of the one it was saved over: its state keeps the fileId it had "+
			"until a start",
			"id", sw.target.ID, "had", sw.src.ID, "err", ierr)
	}
	f.edits.Moved(sw.st, sw.src)
	f.edits.Gone(sw.target.Ino, sw.ts)
	return true, nil
}

func (f *FS) swapBlocked(sw swapping) bool {
	inUse := sw.ts.Writers() > 0 || sw.target.ContentLocal() && f.handles.opened(sw.target.Ino)
	return !sw.src.ContentLocal() || !sw.target.RExists || sw.st.Unrecorded() != nil || sw.ts.Unrecorded() != nil || inUse
}

func (f *FS) dropSwapBase(ctx context.Context, src *meta.Item) (bool, error) {
	if src.Base <= 0 {
		return true, nil
	}
	lacks, err := f.edits.LacksBase(ctx, f.edits.Local(*src))
	if err != nil || lacks {
		return false, err
	}
	if err := f.edits.DropBase(src.Ino); err != nil {
		return false, err
	}
	src.Base, src.BaseTail = 0, false
	return true, nil
}

func (f *FS) swapRows(sw swapping) (bool, error) {
	recorded := false
	err := f.cache.SwapLocal(sw.src.Ino, sw.src.ID, sw.target.Ino, sw.target.ID, func() error {
		err := sw.v.Swap(sw.src, sw.target, sw.parent, sw.name)
		recorded = err == nil
		return err
	})
	if !recorded {
		return false, err
	}
	if err == nil {
		err = f.store.Swapped(sw.target.ID)
	}
	if err != nil {

		f.log.Error("move the content of a file to the fileId it took", "id", sw.target.ID, "had", sw.src.ID, "err", err)
	}
	return true, nil
}

func (f *FS) wholeHere(ctx context.Context, it meta.Item) (bool, error) {
	if !it.ContentLocal() {
		whole, err := f.cache.Whole(it.Ino, it.Size)
		if err != nil || !whole {
			return false, err
		}
		if err := f.edits.MakeDirty(it); err != nil {
			return false, err
		}
		if it, err = f.store.ByIno(it.Ino); err != nil {
			return false, err
		}
	}
	if it.Base == 0 {
		return true, nil
	}
	lacks, err := f.edits.LacksBase(ctx, f.edits.Local(it))
	return !lacks, err
}

func (f *FS) unswap() error {
	moved, err := f.store.Unswapped()
	if err != nil {
		return err
	}
	for as, had := range moved {
		if err := f.cache.MoveLocal(had, as); err != nil {
			return err
		}
		if err := f.store.Swapped(as); err != nil {
			return err
		}
	}
	return nil
}
