package core

import (
	"context"
	"errors"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/edited"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

type sendFailure struct {
	op      meta.Op
	it      meta.Item
	err     error
	unsent  bool
	kind    opKind
	started time.Time
}

type answers struct {
	login, denied, busy, refused, again bool
}

func (f *FS) failed(ctx context.Context, fl sendFailure, done func()) {
	a := f.answersTo(fl)
	if f.undoTry(ctx, fl, a) {
		done()
		return
	}
	attempts := fl.op.Attempts + 1
	next := f.nextTry(fl, a, attempts)
	if serr := f.store.FailOp(fl.op, attempts, next, fl.err.Error(), failureOf(fl.err, a)); serr != nil {
		f.log.Error("save upload failure", "id", fl.it.ID, "err", serr)
	}

	if f.damageAlarm(fl.it, fl.err) || a.login {
		return
	}
	f.log.Warn("upload failed", "id", fl.it.ID, "name", fl.it.Name, "attempt", attempts, "retry_at", next, "err", fl.err)
}

func (f *FS) answersTo(fl sendFailure) answers {
	login := drive.IsLoginExpired(fl.err)
	denied := !fl.unsent && deniedBy(fl.err, fl.kind)
	a := answers{
		login:   login,
		denied:  denied,
		busy:    drive.IsBusy(fl.err),
		refused: !fl.unsent && !login && !denied && drive.IsRefusal(fl.err),
	}
	if !fl.unsent && fl.kind == sendsUpdate && drive.IsPrecondition(fl.err) {

		a.refused = false
		if a.again = f.retries.goesAgain(fl.op.ID); !a.again {
			a.busy = true
		}
	}
	return a
}

func deniedBy(err error, kind opKind) bool {
	return drive.IsDenied(err) || kind == sendsUpdate && drive.IsConflict(err)
}

func (f *FS) undoTry(ctx context.Context, fl sendFailure, a answers) bool {

	asks := fl.it
	fresh, nerr := f.store.ByIno(fl.op.Ino)
	if nerr == nil {
		asks = fresh
	}
	tries := f.undoTries(fl.op, asks, a)
	if tries > 0 && nerr != nil {

		f.log.Warn("read an item before undoing a change Drive does not take: nothing is undone now", "id", fl.it.ID, "err",
			nerr)
		return false
	}
	if tries == 0 {
		return false
	}
	rerr := f.conflicts.revert(ctx, fl.op, tries, fl.err)
	if rerr != nil && ctx.Err() == nil {
		f.log.Error("undo a change Drive does not take", "id", fl.it.ID, "name", fl.it.Name, "err", rerr)
	}
	return rerr == nil
}

func (f *FS) undoTries(op meta.Op, asks meta.Item, a answers) int {
	switch {
	case a.again:
	case a.denied:
		return 1
	case a.refused && op.RefusedWith(asks) >= refusedTries:
		return refusedTries
	case a.busy:

		if n := f.retries.answered(op.ID, op.BusyWith(asks) == 1); n > busyRetries {
			return n
		} else if op.BusyWith(asks) >= answersMax {
			return answersMax
		}
	}
	return 0
}

func (f *FS) nextTry(fl sendFailure, a answers, attempts int) time.Time {
	now := f.clock.Now()
	next := now.Add(backoff(attempts))
	switch {
	case a.again:
		next = now
	case a.busy:
		next = f.retries.waitFor(fl.kind, fl.started, now)
	case a.login:
		f.signedOut(fl.err)
	case errors.Is(fl.err, drive.ErrUploadLimit):
		f.refuse("Drive answered that the daily upload limit is reached")
		next = now.Add(limitBackoff(attempts))
	case errors.Is(fl.err, drive.ErrStorageFull):
		f.refuse("Drive answered that its storage is full")
	}

	if asked := now.Add(drive.WaitAsked(fl.err)); asked.After(next) {
		next = asked
	}
	return next
}

func failureOf(err error, a answers) meta.Failure {
	switch {
	case a.busy:
		return meta.FailedBusy
	case a.refused:
		return meta.FailedRefused
	case a.login, drive.IsNetwork(err):

		return meta.FailedOffline
	}
	return meta.Failed
}

func (f *FS) damageAlarm(it meta.Item, err error) bool {
	if !errors.Is(err, edited.ErrMD5) {
		if st := f.edits.StateIf(it.Ino); st != nil {

			st.Sound()
		}
		return false
	}
	fails := f.edits.State(it).Damaged()
	if fails < md5Alarm {
		return false
	}
	f.log.Error("upload keeps arriving damaged", "id", it.ID, "name", it.Name, "in_a_row", fails)
	return true
}
