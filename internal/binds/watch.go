package binds

import (
	"context"
	"syscall"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/mounts"
)

const watchEvery = time.Second

func (m *Manager) Start(folders Folders) error {
	var st syscall.Stat_t
	if err := syscall.Stat(m.o.MountPoint, &st); err != nil {
		return err
	}
	m.mu.Lock()
	m.folders, m.dev = folders, st.Dev
	promoted := false
	if m.o.Helper != nil {
		for i, r := range m.rows {
			if r.State == Pending {
				m.rows[i].State, promoted = Bound, true
			}
		}
	}
	var err error
	if promoted {
		err = save(m.o.Dir, m.rows)
	}
	m.mu.Unlock()
	m.Tick()
	return err
}

func (m *Manager) Watch(ctx context.Context) {
	tick := time.NewTicker(watchEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			m.Tick()
		}
	}
}

func (m *Manager) Tick() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.folders == nil || m.o.Helper == nil {
		return
	}
	changed := false
	for i := range m.rows {
		if m.look(i) {
			changed = true
		}
	}
	if changed {
		if err := save(m.o.Dir, m.rows); err != nil {
			m.o.Log.Error("write the list of bindings", "err", err)
		}
	}
}

func (m *Manager) look(i int) bool {
	r := m.rows[i]
	if r.State != Bound {
		return false
	}
	f, err := m.folders.Folder(r.Folder)
	switch {
	case err != nil:
		m.tell(r.Target, "find the folder of a binding", err)
	case f.Gone != "":
		return m.ignore(i, f.Gone)
	default:
		m.keep(r, f)
	}
	return false
}

func (m *Manager) ignore(i int, why string) bool {
	r := m.rows[i]
	if err := m.unbindOurs(r.Target); err != nil {
		m.tell(r.Target, "unbind the folder of a binding that left the mount", err)
		return false
	}
	m.rows[i].State, m.rows[i].Reason = Ignored, why
	m.o.Log.Warn("the folder of a binding left the mount: the binding is undone and ignored from now on",
		"at", r.Target, "why", why)
	return true
}

func (m *Manager) keep(r Row, f core.Folder) {
	var st syscall.Stat_t
	if err := syscall.Stat(r.Target, &st); err == nil && st.Dev == m.dev && st.Ino == f.Ino && f.Ino != 0 {
		delete(m.told, r.Target)
		return
	}

	if err := m.unbindOurs(r.Target); err != nil {
		m.tell(r.Target, "unbind a binding that is not of its folder", err)
		return
	}
	if f.Ino == 0 {
		return
	}
	if err := m.o.Helper.Bind(m.source(f), r.Target); err != nil {
		m.tell(r.Target, "bind a folder of the list", err)
		return
	}
	delete(m.told, r.Target)
	m.o.Log.Info("bound a folder of the mount", "folder", f.Path, "at", r.Target)
}

func (m *Manager) tell(target, what string, err error) {
	if m.told[target] == err.Error() {
		return
	}
	m.told[target] = err.Error()
	m.o.Log.Error(what, "at", target, "err", err)
}

func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.rows {
		if err := m.unbindOurs(r.Target); err != nil {
			m.o.Log.Error("unbind a folder of the mount", "at", r.Target, "err", err)
		}
	}
	if c, ok := m.o.Helper.(interface{ Close() error }); ok {
		_ = c.Close()
	}
}

func isOurs(dir string) bool { return (&helper{typesAt: mounts.At}).isOurs(dir) }
