package edited

import (
	"context"
	"errors"
	"fmt"

	"github.com/Ruslan-Linuxoid/FastGD/internal/cache"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func (m *Files) Local(it meta.Item) Local {
	return Local{
		Ino: it.Ino, ID: it.ID, BaseGone: it.RExists && it.RMD5 != it.MD5,
		Check: func(ctx context.Context) error { return m.sameBase(ctx, it) },
	}
}

const baseTries = 3

func (m *Files) fetchBase(ctx context.Context, it meta.Item, off, end int64) (meta.Item, error) {
	for try := 1; !it.ContentLocal(); try++ {
		now, f, err := m.settledClean(it.Ino)
		if err != nil {
			return it, err
		}
		if it = now; it.ContentLocal() {
			break
		}
		if m.beforeFetch != nil {
			m.beforeFetch()
		}
		if err = m.fetchKept(ctx, f, off, end); err == nil {
			return it, nil
		}
		if _, err = m.afterBaseFail(ctx, it, err, try); err != nil {
			return it, err
		}
	}
	return it, m.WithBase(ctx, it.Ino, func() error { return m.FillEdges(ctx, m.Local(it), off, end) })
}

func (m *Files) settledClean(ino meta.Ino) (meta.Item, cache.File, error) {
	release := m.CleanSettled()
	defer release()
	it, err := m.opt.Store.ByIno(ino)
	if err != nil {
		return it, cache.File{}, err
	}
	f := cleanFile(it)
	f.Gen, err = m.clean.Generation(ino)
	return it, f, err
}

func (m *Files) afterBaseFail(ctx context.Context, it meta.Item, err error, try int) (meta.Item, error) {
	if try >= baseTries {
		return it, err
	}
	now, ierr := m.readAnewAfter(ctx, it, err)
	if ierr != nil {
		return it, ierr
	}

	if !errors.Is(err, cache.ErrLocal) && !anotherVersion(err, it, now) {
		return it, err
	}
	if errors.Is(err, cache.ErrLocal) {
		m.Lock(now).Unlock()
		if now, ierr = m.opt.Store.ByIno(it.Ino); ierr != nil {
			return it, ierr
		}
	}
	return now, nil
}

func (m *Files) readAnewAfter(ctx context.Context, it meta.Item, err error) (meta.Item, error) {
	now, ierr := m.opt.Store.ByIno(it.Ino)
	if ierr != nil || m.opt.ReadAnew == nil {
		return now, ierr
	}
	stale := errors.Is(err, cache.ErrStale) && now.Version == it.Version && now.MD5 == it.MD5
	if !stale && !errors.Is(err, cache.ErrMixed) {
		return now, nil
	}
	if rerr := m.opt.ReadAnew(ctx, it.ID); rerr != nil {
		return now, rerr
	}
	return m.opt.Store.ByIno(it.Ino)
}

func anotherVersion(err error, it, now meta.Item) bool {
	return errors.Is(err, cache.ErrStale) || errors.Is(err, cache.ErrMixed) || now.MD5 != it.MD5 ||
		now.Size != it.Size
}

var errAnotherBase = errors.New("edited: the file is another version than the one fetched")

func (m *Files) Write(ctx context.Context, it meta.Item, off int64, data []byte) error {
	end := off + int64(len(data))
	fetch := func(it meta.Item) (meta.Item, error) { return m.fetchBase(ctx, it, off, end) }
	grow := func(size int64) int64 { return max(size, end) }

	_, err := m.changeOnBase(it, "write", fetch, grow, func(st *State, it meta.Item) error {
		if err := m.writeLocal(context.Background(), m.Local(it), data, off); err != nil {
			return err
		}
		st.size = max(st.size, end)
		st.unflushed = true
		return nil
	})
	return err
}

func (m *Files) Truncate(ctx context.Context, it meta.Item, size int64) error {

	fetch := func(it meta.Item) (meta.Item, error) {
		if it.ContentLocal() {
			return it, nil
		}
		return m.fetchBase(ctx, it, size, max(size, it.Size))
	}
	it, err := m.changeOnBase(it, "cut", fetch, func(int64) int64 { return size }, func(st *State, it meta.Item) error {
		if err := m.truncateLocal(it.Ino, it.ID, size); err != nil {
			return err
		}
		st.size = size
		st.unflushed = false
		return nil
	})
	if err != nil {
		return err
	}
	st := m.Lock(it)
	defer st.Unlock()
	return m.persist(it.Ino, st, m.opt.UploadDue(it.Ino))
}

func (m *Files) changeOnBase(
	it meta.Item, what string, fetch func(meta.Item) (meta.Item, error), newSize func(int64) int64,
	change func(*State, meta.Item) error,
) (meta.Item, error) {
	for try := 1; ; try++ {
		based, err := fetch(it)
		if err != nil {

			return it, fmt.Errorf("fetch the Drive version under the %s: %v", what, err)
		}
		err = m.change(based, newSize, change)
		if !errors.Is(err, errAnotherBase) || try >= baseTries {
			return it, err
		}
		if it, err = m.opt.Store.ByIno(it.Ino); err != nil {
			return it, err
		}
	}
}
