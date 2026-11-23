package edited

import (
	"maps"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func (m *Files) AddWriter(it meta.Item) {
	st := m.Lock(it)
	st.writers++
	st.lastWrite = m.opt.Clock.Now()
	st.Unlock()
}

func (m *Files) CloseWriter(ino meta.Ino) {
	st := m.StateIf(ino)
	if st == nil {
		return
	}
	st.mu.Lock()
	st.writers--

	st.flushed(m)
	st.Unlock()
	m.forget(ino, st)
}

func (m *Files) Flushed(ino meta.Ino) {
	st := m.StateIf(ino)
	if st == nil {
		return
	}
	st.mu.Lock()
	if st.unflushed {
		st.flushed(m)
	}
	st.Unlock()
}

func (st *State) flushed(m *Files) {
	st.unflushed = false
	if st.dirty {
		_ = m.persist(st.ino, st, m.opt.UploadDue(st.ino))
	}
}

func (m *Files) Busy(now time.Time, emptyIdle time.Duration) []meta.Ino {
	m.smu.Lock()
	states := maps.Clone(m.states)
	m.smu.Unlock()
	var out []meta.Ino
	for ino, st := range states {
		if !st.mu.TryLock() {
			out = append(out, ino)
			continue
		}
		if st.lastWrite.After(now) {

			st.lastWrite = now
		}
		if st.writers > 0 && (st.unflushed || st.size == 0 && now.Sub(st.lastWrite) < emptyIdle) {
			out = append(out, ino)
		}
		st.Unlock()
	}
	return out
}

func (m *Files) change(it meta.Item, newSize func(size int64) int64, change func(st *State, it meta.Item) error) error {
	based := it
	st := m.Lock(it)
	defer st.Unlock()
	defer st.show()
	it, err := m.readLocked(it.Ino, st)
	if err != nil {
		return err
	}
	if err := m.reserveRoom(it, st, newSize); err != nil {
		return err
	}
	if !st.dirty {
		if it, err = m.markChanged(based, it, st); err != nil {
			return err
		}
	}
	if err := m.dropSession(it, st); err != nil {
		return err
	}
	if err := change(st, it); err != nil {
		return err
	}
	m.count(st, it)
	now := m.opt.Clock.Now()
	st.gen++
	st.lastWrite, st.mtime = now, DriveTime(now)
	return nil
}

func (m *Files) makeDirty(it meta.Item, st *State) (meta.Item, error) {
	m.cleaning.Lock()
	defer m.cleaning.Unlock()
	now, err := m.opt.Store.ByIno(it.Ino)
	if err != nil {
		return it, err
	}
	st.size = now.Size
	tail, err := m.makeLocal(now.Ino, now.ID, now.Size)
	if err != nil {
		return it, err
	}
	if err := m.opt.Store.MarkDirty(now.Ino, now.Size, tail); err != nil {
		return it, err
	}
	st.dirty = true
	now.Dirty, now.Base, now.BaseTail = true, now.Size, tail
	m.count(st, now)
	m.wantBase(now.Ino)
	return now, nil
}

func (m *Files) MakeDirty(it meta.Item) error {
	st := m.Lock(it)
	defer st.Unlock()
	now, err := m.opt.Store.ByIno(it.Ino)
	if err != nil || now.ContentLocal() {
		return err
	}
	_, err = m.makeDirty(now, st)
	st.show()
	return err
}

func (m *Files) persist(ino meta.Ino, st *State, uploadAfter time.Time) error {
	if err := m.opt.Store.SetLocal(ino, st.size, st.mtime, st.gen, uploadAfter); err != nil {
		m.opt.Log.Error("save file state", "ino", ino, "err", err)
		return err
	}
	st.savedGen = st.gen
	return nil
}

func (m *Files) Sync(it meta.Item) error {
	st := m.StateIf(it.Ino)
	if st == nil {
		return nil
	}

	st.mu.Lock()
	dirty := st.dirty
	st.unflushed = false
	st.Unlock()
	if dirty {
		if err := m.syncLocal(it.ID); err != nil {
			return err
		}
	}
	st.mu.Lock()
	defer st.Unlock()
	if !st.dirty {

		return nil
	}
	return m.persist(it.Ino, st, m.opt.UploadDue(it.Ino))
}

func (m *Files) SaveSizes() {
	m.smu.Lock()
	states := maps.Clone(m.states)
	m.smu.Unlock()
	for ino, st := range states {
		if !st.mu.TryLock() {
			continue
		}

		if st.dirty && st.gen != st.savedGen && !st.released {
			if it, err := m.opt.Store.ByIno(ino); err == nil {
				_ = m.persist(ino, st, it.UploadAfter)
			}
		}
		st.Unlock()
	}
}
