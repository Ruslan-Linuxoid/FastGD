package edited

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"

	"github.com/Ruslan-Linuxoid/FastGD/internal/cache"
	"golang.org/x/sys/unix"
)

func (m *Files) fetchKept(ctx context.Context, f cache.File, off, end int64) error {
	kept := keptBase(off, end, f.Size)
	ranges := make([][2]int64, 0, len(kept))
	for _, k := range kept {
		ranges = append(ranges, [2]int64{k.off, k.end})
	}
	if err := m.clean.Fetch(ctx, f, ranges); err != nil {
		return err
	}

	return m.clean.Check(ctx, f)
}

func (m *Files) FillEdges(ctx context.Context, l Local, off, end int64) error {
	w, fd, err := m.openLocal(ctx, l)
	if err != nil {
		return err
	}
	defer fd.Close()
	return m.fillEdges(ctx, w, fd, l, off, end)
}

func (m *Files) fillEdges(ctx context.Context, w *working, fd *os.File, l Local, off, end int64) error {
	for _, e := range edges(off, end, w.baseNow()) {
		if err := m.fillHoles(ctx, w, fd, l, e); err != nil {
			return err
		}
	}
	return nil
}

func (m *Files) fillLocal(ctx context.Context, l Local) error {
	w, fd, err := m.openLocal(ctx, l)
	if err != nil {
		return err
	}
	defer fd.Close()
	for {
		holes, err := w.missing(fd, l.ID, wholeBase)
		if err != nil {
			return err
		}
		if len(holes) == 0 {
			return nil
		}
		if l.BaseGone {
			return ErrBaseGone
		}
		if m.clean.LowDisk() {
			return cache.ErrLowDisk
		}
		h := holes[0]
		h.end = min(h.end, h.off+cache.PageDown(max(m.clean.WindowCap(), holeFlush())))
		if err := m.fillHole(ctx, w, fd, l, h); err != nil {
			return err
		}
	}
}

func (m *Files) fillHoles(ctx context.Context, w *working, fd *os.File, l Local, want span) error {
	holes, err := w.missing(fd, l.ID, func(int64) span { return want })
	if err != nil {
		return err
	}
	if len(holes) > 0 && l.BaseGone {
		return ErrBaseGone
	}
	for _, h := range holes {
		if err := m.fillHole(ctx, w, fd, l, h); err != nil {
			return err
		}
	}
	return nil
}

func (m *Files) fillHole(ctx context.Context, w *working, fd *os.File, l Local, h span) error {
	held, err := os.CreateTemp(m.clean.Dir(), cache.HeldPrefix)
	if err != nil {
		return fmt.Errorf("edited: %w", err)
	}
	defer func() {
		held.Close()
		os.Remove(held.Name())
	}()
	got, broke := m.fetchHeld(ctx, held, l, h)
	if got == 0 {
		return broke
	}
	if l.Check != nil {
		if err := l.Check(ctx); err != nil {
			return err
		}
	}
	if err := m.copyHeld(w, fd, l, held, h.off, got); err != nil {
		return err
	}
	return broke
}

func (m *Files) fetchHeld(ctx context.Context, held *os.File, l Local, h span) (int64, error) {
	from := m.source
	if l.From != nil {
		from = l.From
	}
	var got int64

	broke := from(ctx, l.ID, "", h.off, h.len(), func(b []byte) error {
		if got+int64(len(b)) > h.len() {
			return fmt.Errorf("edited: %s at %d: more bytes than asked for", l.ID, h.off)
		}
		n, err := held.Write(b)
		got += int64(n)
		return err
	})
	if broke != nil {
		got = cache.PageDown(got)
	}
	return got, broke
}

func (m *Files) copyHeld(w *working, fd *os.File, l Local, held *os.File, off, got int64) error {
	buf := make([]byte, holeFlush())
	for pos := off; pos < off+got; {
		b := buf[:min(int64(len(buf)), off+got-pos)]
		if _, err := held.ReadAt(b, pos-off); err != nil {
			return fmt.Errorf("edited: %s at %d: %w", l.ID, pos, err)
		}
		m.clean.Changed()
		w.pages.Lock()
		err := writeHoles(fd, b, pos, w.base)
		if t, ok := w.tailIn(pos, pos+int64(len(b))); err == nil && ok {
			err = m.putTail(w, fd, l, b[t.off-pos:t.end-pos], t.off)
		}
		w.pages.Unlock()
		if err != nil {
			return fmt.Errorf("edited: write %s at %d: %w", l.ID, pos, err)
		}
		pos += int64(len(b))
	}
	return nil
}

func (m *Files) putTail(w *working, fd *os.File, l Local, b []byte, off int64) error {
	if _, err := fd.WriteAt(b, off); err != nil {
		return err
	}
	if err := unix.Fdatasync(int(fd.Fd())); err != nil {
		return err
	}
	return m.tailFilled(w, l)
}

func (w *working) tailIn(off, end int64) (span, bool) {
	t := tailOf(w.base)
	return t, w.tail && off <= t.off && end >= t.end
}

func (m *Files) tailFilled(w *working, l Local) error {
	if err := m.store.SetBase(l.Ino, w.base, false); err != nil {
		return err
	}
	w.tail = false
	return nil
}

func writeHoles(fd *os.File, b []byte, off, base int64) error {
	holes, err := holesIn(fd, span{off, min(off+int64(len(b)), base)})
	if err != nil {
		return err
	}
	for _, h := range holes {
		if _, err := fd.WriteAt(b[h.off-off:h.end-off], h.off); err != nil {
			return err
		}
	}
	return nil
}

func holesIn(fd *os.File, s span) ([]span, error) {
	var out []span
	for pos := s.off; pos < s.end; {
		data, err := unix.Seek(int(fd.Fd()), pos, unix.SEEK_DATA)
		if errors.Is(err, syscall.ENXIO) {

			out = append(out, span{pos, s.end})
			break
		}
		if err != nil {
			return nil, err
		}
		if data > pos {
			out = append(out, span{pos, min(data, s.end)})
		}
		if data >= s.end {
			break
		}
		if pos, err = unix.Seek(int(fd.Fd()), data, unix.SEEK_HOLE); err != nil {
			return nil, err
		}
	}
	return out, nil
}
