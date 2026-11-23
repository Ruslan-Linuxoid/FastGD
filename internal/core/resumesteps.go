package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/edited"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

type resumeUpload struct {
	it      meta.Item
	st      *edited.State
	snap    snapshot
	create  bool
	ses     meta.Session
	p       drive.Progress
	tail    sealed
	renewed bool
	stalls  int
}

func (f *FS) resumeStep(ctx, traced context.Context, u *resumeUpload) error {
	if u.st.Outdated(u.snap.gen) {
		return errOutdated
	}
	if err := f.sealWhenDue(u); err != nil {
		return err
	}
	next, err := f.nextProgress(ctx, traced, u)
	switch {
	case errors.Is(err, drive.ErrSessionGone):
		return f.sessionGone(traced, u, err)
	case err != nil:
		return err
	}
	return u.advance(next)
}

func (f *FS) sealWhenDue(u *resumeUpload) error {
	if u.tail.bytes != nil || u.p.Confirmed < tailFrom(u.snap.size) {
		return nil
	}
	var err error
	u.tail, err = f.seal(u.st, u.snap)
	return err
}

func (f *FS) nextProgress(ctx, traced context.Context, u *resumeUpload) (drive.Progress, error) {
	next, err := f.sendChunk(ctx, u.ses.Address, u.p.Confirmed, u.snap, u.tail)
	if errors.Is(err, drive.ErrUploadLimit) || errors.Is(err, drive.ErrStorageFull) {

		return next, err
	}
	if err != nil && !errors.Is(err, drive.ErrSessionGone) && ctx.Err() == nil {
		next, err = f.opt.Drive.UploadStatus(traced, u.ses.Address, u.snap.size)
	}
	return next, err
}

func (f *FS) sessionGone(traced context.Context, u *resumeUpload, err error) error {
	if u.renewed || errors.Is(err, drive.ErrSessionRefused) {

		if cerr := f.store.ClearUploadSession(u.it.Ino); cerr != nil {
			return cerr
		}
		return err
	}

	u.renewed = true
	f.log.Info("Drive forgot the upload session, the upload starts again", "id", u.it.ID)
	ses, err := f.newSession(traced, u.it, u.st, u.snap, u.create)
	if err != nil {
		return err
	}
	u.ses, u.p, u.stalls = ses, drive.Progress{}, 0
	return nil
}

func (u *resumeUpload) advance(next drive.Progress) error {
	if next.Done == nil && next.Confirmed <= u.p.Confirmed {
		if u.stalls++; u.stalls == maxStalls {
			return fmt.Errorf("drive took no byte of %d chunks in a row at %d", maxStalls, u.p.Confirmed)
		}
	} else {
		u.stalls = 0
	}
	u.p = next
	return nil
}
