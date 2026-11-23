package edited

import (
	"errors"
	"maps"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func (m *Files) count(st *State, it meta.Item) {
	want := int64(0)
	if st.dirty && !st.released && !it.Trashed && !it.LocalOnly() {
		want = st.size
	}
	m.waiting.Add(want - st.counted)
	st.counted = want
}

func (m *Files) Recount() {
	m.smu.Lock()
	states := maps.Clone(m.states)
	m.smu.Unlock()
	for _, st := range states {
		m.recount(st)
	}
}

func (m *Files) Recounted(ino meta.Ino) {
	if st := m.StateIf(ino); st != nil {
		m.recount(st)
	}
}

func (m *Files) recount(st *State) {
	if !st.mu.TryLock() {
		st.stale.Store(true)
		return
	}
	defer st.mu.Unlock()
	m.recounted(st)
}

func (m *Files) recounted(st *State) {
	it, err := m.opt.Store.ByIno(st.ino)
	switch {
	case errors.Is(err, meta.ErrNotFound):

		it = meta.Item{Trashed: true}
	case err != nil:

		m.opt.Log.Warn("count the bytes that wait for Drive", "ino", st.ino, "err", err)
		st.stale.Store(true)
		return
	}
	m.count(st, it)
}

func (m *Files) forget(ino meta.Ino, st *State) {
	st.mu.Lock()
	defer st.Unlock()
	if st.dirty || st.writers > 0 {
		return
	}
	m.smu.Lock()
	defer m.smu.Unlock()
	if m.states[ino] == st {
		delete(m.states, ino)
		st.released = true
	}
}

func (m *Files) Drop(ino meta.Ino) {
	st := m.StateIf(ino)
	if st == nil || !st.mu.TryLock() {
		if st != nil {
			st.dropped.Store(true)
		}
		m.Gone(ino, nil)
		return
	}
	m.Gone(ino, st)
	st.Unlock()
}

func (m *Files) Gone(ino meta.Ino, st *State) {
	m.smu.Lock()
	if st == nil || m.states[ino] == st {
		delete(m.states, ino)
	}
	delete(m.notTakenUp, ino)
	m.smu.Unlock()
	m.forgetCopy(ino)
	if st != nil {
		st.released = true
		m.count(st, meta.Item{})
	}
}
