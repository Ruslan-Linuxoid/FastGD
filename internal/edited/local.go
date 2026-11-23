package edited

import (
	"context"
	"fmt"
	"os"

	"github.com/Ruslan-Linuxoid/FastGD/internal/cache"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func (m *Files) truncateLocal(ino meta.Ino, id string, size int64) error {
	w := m.working(ino)
	w.pages.Lock()
	defer w.pages.Unlock()
	fd, err := os.OpenFile(m.clean.Path(id), os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("edited: %w", err)
	}
	defer fd.Close()
	if err := fd.Truncate(size); err != nil {
		return fmt.Errorf("edited: truncate %s: %w", id, err)
	}
	m.clean.Changed()
	if size >= w.base {
		return nil
	}

	tail := w.tail && size/page() == w.base/page()
	if !tail {
		if tail, err = tailMissing(fd, size); err != nil {
			return fmt.Errorf("edited: %s: %w", id, err)
		}
	}
	if err := m.syncFile(fd); err != nil {
		return fmt.Errorf("edited: sync %s: %w", id, err)
	}
	if err := m.store.SetBase(ino, size, tail); err != nil {
		return err
	}
	w.base, w.tail = size, tail
	return nil
}

func (m *Files) writeLocal(ctx context.Context, l Local, p []byte, off int64) error {
	w, fd, err := m.openLocal(ctx, l)
	if err != nil {
		return err
	}
	defer fd.Close()
	m.clean.Touched(l.Ino)
	defer m.clean.Changed()
	end := off + int64(len(p))
	if err := m.fillEdges(ctx, w, fd, l, off, end); err != nil {
		return err
	}
	w.pages.Lock()
	defer w.pages.Unlock()
	if _, err := fd.WriteAt(p, off); err != nil {
		return fmt.Errorf("edited: write %s at %d: %w", l.ID, off, err)
	}

	if _, ok := w.tailIn(off, end); ok {
		return m.tailFilled(w, l)
	}
	return nil
}

func (m *Files) ReadLocal(ctx context.Context, l Local, p []byte, off int64) (int, error) {
	w, fd, err := m.openLocal(ctx, l)
	if err != nil {
		return 0, err
	}
	defer fd.Close()
	fi, err := fd.Stat()
	if err != nil {
		return 0, fmt.Errorf("edited: %w", err)
	}
	if off >= fi.Size() || len(p) == 0 {
		return 0, nil
	}
	m.clean.Touched(l.Ino)
	end := min(off+int64(len(p)), fi.Size())
	want := pageSpan(off, end, fi.Size())
	if m.clean.LowDisk() {

		holes, err := w.missing(fd, l.ID, func(int64) span { return want })
		if err != nil {
			return 0, err
		}
		if len(holes) > 0 {
			return 0, cache.ErrLowDisk
		}
	}
	if err := m.fillHoles(ctx, w, fd, l, want); err != nil {
		return 0, err
	}
	if _, err := fd.ReadAt(p[:end-off], off); err != nil {
		return 0, fmt.Errorf("edited: read %s at %d: %w", l.ID, off, err)
	}
	return int(end - off), nil
}

func (m *Files) LacksBase(ctx context.Context, l Local) (bool, error) {
	w, fd, err := m.openLocal(ctx, l)
	if err != nil {
		return false, err
	}
	defer fd.Close()
	holes, err := w.missing(fd, l.ID, wholeBase)
	if err != nil {
		return false, err
	}
	return len(holes) > 0, nil
}

func (m *Files) DropBase(ino meta.Ino) error {
	return m.DropBaseIn(ino, func() error { return m.store.SetBase(ino, 0, false) })
}

func (m *Files) DropBaseIn(ino meta.Ino, record func() error) error {
	w := m.working(ino)
	w.pages.Lock()
	defer w.pages.Unlock()
	if err := record(); err != nil {
		return err
	}
	w.base, w.tail = 0, false
	return nil
}
