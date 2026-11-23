package binds

import (
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
)

var ErrNoRoot = errors.New("binding folders needs the daemon to start as root, as the systemd service starts it")

type Folders interface {
	FolderAt(path string) (string, error)
	Folder(id string) (core.Folder, error)
}

type Binder interface {
	Bind(source, target string) error
	Unbind(target string) error
}

type Options struct {
	Dir string

	MountPoint string

	Root bool

	Helper Binder
	Log    *slog.Logger
}

type Manager struct {
	o       Options
	mu      sync.Mutex
	rows    []Row
	folders Folders

	dev uint64

	told map[string]string

	ours func(dir string) bool

	broken error
}

func New(o Options) *Manager {
	rows, err := Load(o.Dir)
	if err != nil {
		o.Log.Error("the folders of the list are not bound", "err", err)
	}
	return &Manager{o: o, rows: rows, told: map[string]string{}, ours: isOurs, broken: err}
}

type Added string

const (
	AddedBound   Added = "bound"
	AddedPending Added = "added: the list was empty at the start of the daemon, so the folder is bound from " +
		"its next start on"
)

func (m *Manager) Add(path, target string) (Added, error) {
	if err := m.refusal(); err != nil {
		return "", err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.folders == nil {
		return "", errors.New("the mount is not up yet")
	}
	id, err := m.folders.FolderAt(path)
	if err != nil {
		return "", err
	}
	target, err = m.checkTarget(target)
	if err != nil {
		return "", err
	}
	row := Row{Folder: id, Target: target, State: Pending}
	if m.o.Helper != nil {
		row.State = Bound
		if err := m.bindNew(row); err != nil {
			return "", err
		}
	}
	m.rows = append(m.rows, row)
	if err := save(m.o.Dir, m.rows); err != nil {
		m.rows = m.rows[:len(m.rows)-1]
		if uerr := m.unbindOurs(target); uerr != nil {
			m.o.Log.Error("unbind a folder of the mount", "at", target, "err", uerr)
		}
		return "", err
	}
	if row.State == Pending {
		return AddedPending, nil
	}
	return AddedBound, nil
}

func (m *Manager) Remove(target string) error {
	if m.broken != nil {
		return m.broken
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	target = placeOf(target)
	i := slices.IndexFunc(m.rows, func(r Row) bool { return r.Target == target })
	if i < 0 {
		return fmt.Errorf("%s is not in the list", target)
	}
	if err := m.unbindOurs(target); err != nil {
		return err
	}
	rows := slices.Delete(slices.Clone(m.rows), i, i+1)
	if err := save(m.o.Dir, rows); err != nil {
		return err
	}
	m.rows = rows
	delete(m.told, target)
	return nil
}

type Listed struct {
	Row

	Path string `json:"path"`
}

func (m *Manager) List() ([]Listed, error) {
	if m.broken != nil {
		return nil, m.broken
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Listed, 0, len(m.rows))
	for _, r := range m.rows {
		l := Listed{Row: r}
		if m.folders != nil && r.State != Ignored {
			if f, err := m.folders.Folder(r.Folder); err == nil && f.Gone == "" {
				l.Path = f.Path
			}
		}
		out = append(out, l)
	}
	return out, nil
}

func (m *Manager) refusal() error {
	if m.broken != nil {
		return m.broken
	}
	if !m.o.Root {
		return ErrNoRoot
	}
	return nil
}

func (m *Manager) checkTarget(target string) (string, error) {
	if !filepath.IsAbs(target) {
		return "", fmt.Errorf("%s: %w: not an absolute path", target, ErrPlaceTaken)
	}
	if _, err := filepath.EvalSymlinks(filepath.Dir(filepath.Clean(target))); err != nil {
		return "", fmt.Errorf("%s: %w: the folder it would be made in is not there", target, ErrPlaceTaken)
	}
	target = placeOf(target)
	if within(target, m.o.MountPoint) {
		return "", fmt.Errorf("%s: %w: it is inside the mount of the daemon", target, ErrPlaceTaken)
	}
	for _, r := range m.rows {
		if within(target, r.Target) {
			return "", fmt.Errorf("%s: %w: it is inside the place of another binding, %s", target, ErrPlaceTaken, r.Target)
		}
	}
	if _, err := checkPlace(target); err != nil {
		return "", err
	}
	return target, nil
}

func within(path, dir string) bool { return path == dir || strings.HasPrefix(path, dir+"/") }

func (m *Manager) bindNew(row Row) error {
	f, err := m.folders.Folder(row.Folder)
	if err != nil {
		return err
	}
	if f.Ino == 0 {

		return nil
	}
	return m.o.Helper.Bind(m.source(f), row.Target)
}

func (m *Manager) source(f core.Folder) string { return filepath.Join(m.o.MountPoint, f.Path) }

func (m *Manager) unbindOurs(target string) error {
	if m.o.Helper == nil || !m.ours(target) {
		return nil
	}
	return m.o.Helper.Unbind(target)
}

func placeOf(target string) string {
	target = filepath.Clean(target)
	if above, err := filepath.EvalSymlinks(filepath.Dir(target)); err == nil {
		return filepath.Join(above, filepath.Base(target))
	}
	return target
}
