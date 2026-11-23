package edited

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"

	"github.com/Ruslan-Linuxoid/FastGD/internal/cache"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

type Clean interface {
	CleanBytes

	Path(id string) string
	Dir() string

	HandOver(ino meta.Ino, id string, base int64) error

	MarkEdited(ino meta.Ino) error

	LowDisk() bool

	Touched(ino meta.Ino)
	Changed()

	WindowCap() int64
}

type CleanBytes interface {
	Generation(ino meta.Ino) (uint64, error)

	Fetch(ctx context.Context, f cache.File, ranges [][2]int64) error

	Check(ctx context.Context, f cache.File) error
}

type Store interface {
	SetBase(ino meta.Ino, base int64, tail bool) error

	ByIno(ino meta.Ino) (meta.Item, error)
}

type Files struct {
	edits

	clean  Clean
	store  Store
	source cache.Source

	mu     sync.Mutex
	copies map[meta.Ino]*working

	syncFile func(*os.File) error
}

type working struct {
	pages sync.Mutex

	base int64
	tail bool

	ready chan struct{}
}

func New(clean Clean, store Store, source cache.Source) *Files {
	return &Files{clean: clean, store: store, source: source, copies: map[meta.Ino]*working{}, syncFile: (*os.File).Sync}
}

func (m *Files) working(ino meta.Ino) *working {
	m.mu.Lock()
	defer m.mu.Unlock()
	w := m.copies[ino]
	if w == nil {
		w = &working{}
		m.copies[ino] = w
	}
	return w
}

func (m *Files) forgetCopy(ino meta.Ino) {
	m.mu.Lock()
	delete(m.copies, ino)
	m.mu.Unlock()
}

func (m *Files) makeLocal(ino meta.Ino, id string, base int64) (tail bool, err error) {
	w := m.working(ino)
	ready := make(chan struct{})
	w.pages.Lock()
	w.ready = ready
	w.pages.Unlock()
	defer func() {
		w.pages.Lock()
		w.ready = nil
		if err == nil {
			w.base, w.tail = base, tail
		}
		w.pages.Unlock()
		close(ready)
	}()
	if err := m.clean.HandOver(ino, id, base); err != nil {
		return false, err
	}
	fd, err := os.Open(m.clean.Path(id))
	if err != nil {
		return false, fmt.Errorf("edited: %w", err)
	}
	defer fd.Close()
	if tail, err = tailMissing(fd, base); err != nil {
		return false, fmt.Errorf("edited: %s: %w", id, err)
	}
	return tail, nil
}

func (m *Files) resumeLocal(ino meta.Ino, base int64, tail bool) error {
	if err := m.clean.MarkEdited(ino); err != nil {
		return err
	}
	w := m.working(ino)
	w.pages.Lock()
	w.base, w.tail = base, tail
	w.pages.Unlock()
	return nil
}

type Local struct {
	Ino meta.Ino
	ID  string

	BaseGone bool

	From cache.Source

	Check func(context.Context) error
}

var ErrBaseGone = errors.New("edited: the file changed in Drive " +
	"before the parts of its version not cached were fetched")

func (m *Files) openLocal(ctx context.Context, l Local) (*working, *os.File, error) {
	w := m.working(l.Ino)
	w.pages.Lock()
	ready := w.ready
	w.pages.Unlock()
	if ready != nil {
		select {
		case <-ready:
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}
	fd, err := os.OpenFile(m.clean.Path(l.ID), os.O_RDWR, 0)
	if errors.Is(err, fs.ErrNotExist) {

		if it, ierr := m.store.ByIno(l.Ino); ierr == nil && it.ID != l.ID {
			fd, err = os.OpenFile(m.clean.Path(it.ID), os.O_RDWR, 0)
		}
	}
	if err != nil {
		return nil, nil, fmt.Errorf("edited: %w", err)
	}
	return w, fd, nil
}
