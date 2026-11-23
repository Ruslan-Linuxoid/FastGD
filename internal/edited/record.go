package edited

import (
	"errors"
	"fmt"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

type Outcome int

const (
	Kept Outcome = iota

	Rewritten

	Cleaned
)

func (m *Files) Record(st *State, up Upload) (out Outcome, err error) {
	st.mu.Lock()
	defer st.Unlock()
	st.unrecorded = nil
	defer func() {
		if err != nil && !errors.Is(err, ErrMD5) {
			st.unrecorded = &up
		}
	}()
	if m.beforeRecord != nil {
		if err = m.beforeRecord(); err != nil {
			return Kept, err
		}
	}
	var done bool
	if out, done, err = m.vetUpload(st, up); done {
		return out, err
	}
	it := up.Item
	if up.Same {

		if err = m.opt.Marks.SetMtime(it.Ino, st.mtime); err != nil {
			return Kept, err
		}
	}
	if err = m.opt.Marks.MarkClean(it.Ino, up.Remote, up.SentMeta, up.Size); err != nil {
		return Kept, err
	}
	m.cleaned(it.Ino, st)
	if aerr := m.opt.Disk.Adopt(it.Ino, up.Size); aerr != nil {

		m.opt.Log.Warn("mark the uploaded content cached", "id", it.ID, "err", aerr)
	}
	return Cleaned, nil
}

func (m *Files) vetUpload(st *State, up Upload) (Outcome, bool, error) {
	it := up.Item
	if it.Base > 0 {

		if err := m.DropBase(it.Ino); err != nil {
			return Kept, true, err
		}
	}
	if st.gen != up.Gen {

		return Rewritten, true, m.opt.Marks.MarkCreated(it.Ino, up.Remote, up.SentMeta)
	}
	now, err := m.opt.Store.ByIno(it.Ino)
	if err != nil {
		return Kept, true, err
	}
	if now.RMD5 != up.Remote.MD5 && now.RVersion > up.Remote.Version {

		return Kept, true, m.opt.Marks.MarkCreated(it.Ino, up.Remote, up.SentMeta)
	}
	if up.Remote.MD5 != up.SentMD5 {
		if err = m.opt.Marks.MarkCreated(it.Ino, up.Remote, up.SentMeta); err != nil {
			return Kept, true, err
		}
		return Kept, true, fmt.Errorf("%w: sent %s, Drive has %s", ErrMD5, up.SentMD5, up.Remote.MD5)
	}
	st.md5Fails = 0
	return 0, false, nil
}

func (m *Files) cleaned(ino meta.Ino, st *State) {
	st.dirty = false
	st.session, st.unrecorded = false, nil
	st.show()
	m.count(st, meta.Item{})
	m.forgetCopy(ino)

	go m.forget(ino, st)
}

func (m *Files) GaveWay(ino meta.Ino, st *State) {
	if st.dirty {
		m.cleaned(ino, st)
		return
	}
	st.session, st.unrecorded = false, nil
	go m.forget(ino, st)
}

func (m *Files) Moved(st *State, it meta.Item) {
	st.session, st.unrecorded = false, nil
	st.dirty = true
	st.show()
	m.count(st, it)
}
