package edited

import (
	"errors"
	"fmt"
	"io/fs"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func (m *Files) Recover() error {
	items, err := m.opt.Store.Dirty()
	if err != nil {
		return err
	}
	for _, it := range items {
		err := m.takeUp(it)
		if errors.Is(err, errNotTakenUp) {
			m.opt.Log.Error("the cached content of a dirty file cannot be looked at: it stays out of Drive until it can", "id",
				it.ID, "name", it.Name, "err", err)
			m.smu.Lock()
			m.notTakenUp[it.Ino] = true
			m.smu.Unlock()

			st := m.Lock(it)
			m.count(st, it)
			st.Unlock()
			continue
		}
		if err != nil {
			return err
		}
	}
	return nil
}

const (
	StepSize = "size"

	StepResume = "resume"
)

func (m *Files) takeUp(it meta.Item) error {
	size, err := m.sizeOnDisk(it)
	if err != nil {
		return err
	}

	if err := m.opt.Store.SetLocal(it.Ino, size, it.Mtime, it.Gen, time.Time{}); err != nil {
		return err
	}
	if size != it.Size && m.opt.Resized != nil {
		m.opt.Resized(it.Ino)
	}
	it.Size = size
	if err := m.resumeUp(it, size); err != nil {
		return err
	}
	ses, err := m.opt.Store.UploadSession(it.Ino)
	if err != nil {
		return err
	}

	st := m.Lock(it)
	st.size, st.session = size, ses.Address != ""
	st.show()
	m.count(st, it)
	st.Unlock()
	m.smu.Lock()
	delete(m.notTakenUp, it.Ino)
	m.smu.Unlock()
	return nil
}

func (m *Files) fault(step string, it meta.Item) error {
	if m.opt.RecoverFault == nil {
		return nil
	}
	return m.opt.RecoverFault(step, it)
}

func (m *Files) sizeOnDisk(it meta.Item) (int64, error) {
	size, err := m.opt.Disk.LocalSize(it.ID)
	if err == nil {
		err = m.fault(StepSize, it)
	}
	switch {
	case errors.Is(err, fs.ErrNotExist) && !it.RExists:

		if err := m.opt.Disk.CreateLocal(it.ID); err != nil {
			return 0, fmt.Errorf("%w: no content in the cache, and none can be made: %w", errNotTakenUp, err)
		}
		return 0, nil
	case errors.Is(err, fs.ErrNotExist):

		return 0, fmt.Errorf("%w: its cached content is lost", errNotTakenUp)
	case err != nil:
		return 0, fmt.Errorf("%w: %w", errNotTakenUp, err)
	}
	return size, nil
}

func (m *Files) resumeUp(it meta.Item, size int64) error {

	if err := m.fault(StepResume, it); err != nil {
		return err
	}
	if err := m.resumeLocal(it.Ino, min(it.Base, size), it.BaseTail); err != nil {
		return err
	}
	if it.Base > 0 {
		m.wantBase(it.Ino)
	}
	return nil
}

func (m *Files) TakenUp(it meta.Item) (meta.Item, error) {
	waiting := func() bool {
		m.smu.Lock()
		defer m.smu.Unlock()
		return m.notTakenUp[it.Ino]
	}
	if !waiting() {
		return it, nil
	}
	m.takingUp.Lock()
	defer m.takingUp.Unlock()
	if waiting() {
		if err := m.takeUp(it); err != nil {
			return it, err
		}
	}
	return m.opt.Store.ByIno(it.Ino)
}
