package edited

import (
	"context"
	"errors"
	"fmt"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

type Drive interface {
	Get(ctx context.Context, id string) (*drive.File, error)
	KeepRevision(ctx context.Context, id, rev string, keep bool) error
	StreamRevision(ctx context.Context, id, rev string, off, length int64, sink func([]byte) error) error
}

var ErrBaseLost = errors.New("the Drive version the change is based on cannot be fetched any more")

func (m *Files) FetchBase(ctx context.Context, ino meta.Ino) error {

	ctx = drive.Promptly(ctx)
	it, ok, err := m.basedItem(ino)
	if !ok {
		return err
	}

	release, err := m.holdBasing(ctx, m.State(it))
	if err != nil {
		return err
	}
	defer release()
	if it, ok, err = m.basedItem(ino); !ok {
		return err
	}
	l := Local{Ino: it.Ino, ID: it.ID}
	lacks, err := m.LacksBase(ctx, l)
	if err != nil {
		return err
	}
	if !lacks {

		return m.unkeep(ctx, it)
	}
	it, done, err := m.fetchFromFile(ctx, it, l)
	if done {
		return err
	}
	return m.fetchRevision(ctx, it, l)
}

func (m *Files) basedItem(ino meta.Ino) (meta.Item, bool, error) {
	it, err := m.opt.Store.ByIno(ino)
	if err != nil || !it.Dirty || it.Base == 0 {
		return it, false, err
	}
	return it, true, nil
}

func (m *Files) holdBasing(ctx context.Context, st *State) (func(), error) {
	select {
	case st.basing <- struct{}{}:
		return func() { <-st.basing }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (m *Files) fetchFromFile(ctx context.Context, it meta.Item, l Local) (meta.Item, bool, error) {
	rf, err := m.opt.Drive.Get(ctx, it.ID)
	if err != nil && !errors.Is(err, drive.ErrNotFound) {
		return it, true, err
	}
	if err == nil && rf.MD5 == it.MD5 {
		if it.Rev != rf.HeadRevision {

			if err := m.opt.Marks.SetRev(it.Ino, rf.HeadRevision); err != nil {
				return it, true, err
			}
			it.Rev = rf.HeadRevision
		}
		l.Check = func(ctx context.Context) error { return m.sameBase(ctx, it) }
		if err := m.fillLocal(ctx, l); !errors.Is(err, ErrBaseGone) {
			return it, true, err
		}
	}
	return it, false, nil
}

func (m *Files) WithBase(ctx context.Context, ino meta.Ino, op func() error) error {
	err := op()
	if !errors.Is(err, ErrBaseGone) {
		return err
	}
	if err := m.FetchBase(ctx, ino); err != nil {
		return err
	}
	return op()
}

func (m *Files) fetchRevision(ctx context.Context, it meta.Item, l Local) error {
	if it.Rev == "" {
		return fmt.Errorf("%w: Drive did not tell its revision", ErrBaseLost)
	}
	lost := func(err error) error {
		if errors.Is(err, drive.ErrNoRevision) {
			return fmt.Errorf("%w: Drive deleted revision %s", ErrBaseLost, it.Rev)
		}
		return err
	}
	l.From = func(ctx context.Context, id, _ string, off, length int64, sink func([]byte) error) error {
		return m.opt.Drive.StreamRevision(ctx, id, it.Rev, off, length, sink)
	}
	if err := m.fillLocal(ctx, l); err != nil {
		return lost(err)
	}
	m.opt.Log.Info("fetched the rest of a changed file's Drive version from its revision", "id", it.ID, "name", it.Name,
		"revision", it.Rev)
	return m.unkeep(ctx, it)
}

func (m *Files) UnkeepIdle(ctx context.Context) {
	kept, err := m.opt.Marks.KeptRevisions()
	if err != nil {
		m.opt.Log.Warn("list the revisions marked Keep Forever", "err", err)
		return
	}
	for id := range kept {
		it, err := m.opt.Store.ByID(id)
		switch {
		case errors.Is(err, meta.ErrNotFound):
			it = meta.Item{ID: id}
		case err != nil || it.Dirty && it.Base != 0:

			continue
		}
		if err := m.unkeep(ctx, it); err != nil && ctx.Err() == nil {
			m.opt.Log.Warn("remove the mark Keep Forever of a file that needs it no more", "id", id, "err", err)
		}
	}
}

func (m *Files) unkeep(ctx context.Context, it meta.Item) error {
	rev, err := m.opt.Marks.KeptRevision(it.ID)
	if err != nil || rev == "" {
		return err
	}
	err = m.opt.Drive.KeepRevision(ctx, it.ID, rev, false)
	if err != nil && (drive.IsNetwork(err) || ctx.Err() != nil) {
		return err
	}
	if err != nil && !errors.Is(err, drive.ErrNoRevision) {
		m.opt.Log.Warn("the revision stays marked Keep Forever in Drive", "id", it.ID, "name", it.Name, "revision", rev,
			"err", err)
	}
	return m.opt.Marks.ForgetKeptRevision(it.ID)
}

func (m *Files) FillBase(ctx context.Context, it meta.Item) error {
	if it.Base == 0 {
		return nil
	}
	if err := m.FetchBase(ctx, it.Ino); err != nil {
		return err
	}
	return m.sameBase(ctx, it)
}

func (m *Files) sameBase(ctx context.Context, it meta.Item) error {
	rf, err := m.opt.Drive.Get(ctx, it.ID)
	if err != nil {
		return err
	}
	if rf.MD5 != it.MD5 {
		return fmt.Errorf("%w: based on md5 %s, Drive has %s", ErrBaseGone, it.MD5, rf.MD5)
	}
	return nil
}
