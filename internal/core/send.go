package core

import (
	"context"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func (f *FS) send(ctx context.Context, op meta.Op) {
	defer f.finished(op.Ino)
	done := func() {
		f.retries.forget(op.ID)
		if err := f.store.DoneOp(op); err != nil {
			f.log.Error("finish an operation", "id", op.ID, "err", err)
		}
	}

	it, err := f.store.ByIno(op.Ino)
	if err != nil || it.Standing() == meta.GoneUnsent {
		done()
		return
	}
	it, err = f.withFileID(ctx, it)

	unsent := err != nil
	kind, started := kindOf(it), f.clock.Now()
	if err == nil && it.NeverTried() {

		err = f.store.MarkCreateTried(it.Ino)
		it.CreateTried = true
	}
	if err == nil {
		err = f.reconcileCounting(ctx, op, it)
	}
	if err == nil {
		f.retries.passed(kind)
		f.signedIn()
		done()
		return
	}
	if ctx.Err() != nil {
		f.queue.requeue(op)
		return
	}
	f.failed(ctx, sendFailure{op: op, it: it, err: err, unsent: unsent, kind: kind, started: started}, done)
}

func (f *FS) withFileID(ctx context.Context, it meta.Item) (meta.Item, error) {
	if meta.IsLocalKey(it.ID) && it.Standing() != meta.OnlyHere {
		return f.giveID(ctx, it)
	}
	return it, nil
}

func (f *FS) reconcileCounting(ctx context.Context, op meta.Op, it meta.Item) error {

	noted := false

	tries := drive.OnRetry(drive.Deferring(ctx), func(err error, wait time.Duration) {
		noted = true
		if serr := f.store.NoteTry(op, op.Attempts+1, f.clock.Now().Add(wait), err.Error()); serr != nil {
			f.log.Error("save upload failure", "id", it.ID, "err", serr)
		}
	})
	err := f.reconcile(tries, it)
	if err == nil && noted {

		if serr := f.store.TakeBackTry(op); serr != nil {
			f.log.Error("save upload failure", "id", it.ID, "err", serr)
		}
	}
	return err
}
