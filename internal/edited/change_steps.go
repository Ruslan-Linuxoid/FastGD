package edited

import (
	"errors"
	"fmt"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func (m *Files) markChanged(based, it meta.Item, st *State) (meta.Item, error) {
	if based.ContentLocal() || based.MD5 != it.MD5 || based.Size != it.Size {
		return it, errAnotherBase
	}
	it, err := m.makeDirty(it, st)
	if err != nil {
		return it, fmt.Errorf("mark the file changed here: %w", err)
	}
	return it, nil
}

func (m *Files) dropSession(it meta.Item, st *State) error {
	if !st.session {
		return nil
	}
	if err := m.opt.Store.ClearUploadSession(it.Ino); err != nil {
		return fmt.Errorf("drop the upload session: %w", err)
	}
	st.session = false
	return nil
}

func (m *Files) readLocked(ino meta.Ino, st *State) (meta.Item, error) {
	it, err := m.opt.Store.ByIno(ino)
	if errors.Is(err, meta.ErrNotFound) {

		m.Gone(st.ino, st)
	}
	if err != nil {
		return it, err
	}

	st.dirty = it.ContentLocal()
	if it.Denied.Has(meta.DenyModify) {
		return it, ErrDenied
	}
	if !st.dirty {
		st.size = it.Size
	}
	return it, nil
}

func (m *Files) reserveRoom(it meta.Item, st *State, newSize func(size int64) int64) error {
	if m.opt.Room == nil {
		return nil
	}
	old := int64(0)
	if st.dirty {
		old = st.size
	}
	return m.opt.Room(it, old, newSize(st.size))
}
