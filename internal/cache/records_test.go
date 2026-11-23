package cache

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/clock"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

type memRecords struct {
	mu     sync.Mutex
	files  map[string]meta.Item
	pieces map[meta.Ino][]meta.Piece
}

func newMemRecords(files ...meta.Item) *memRecords {
	m := &memRecords{files: map[string]meta.Item{}, pieces: map[meta.Ino][]meta.Piece{}}
	for _, it := range files {
		m.files[it.ID] = it
	}
	return m
}

func (m *memRecords) Knows(id string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.files[id]
	return ok, nil
}

func (m *memRecords) ByID(id string) (meta.Item, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	it, ok := m.files[id]
	if !ok {
		return meta.Item{}, meta.ErrNotFound
	}
	return it, nil
}

func (m *memRecords) DirtyIDs() ([]string, error) {
	items, err := m.Dirty()
	ids := make([]string, len(items))
	for i, it := range items {
		ids[i] = it.ID
	}
	return ids, err
}

func (m *memRecords) Dirty() ([]meta.Item, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []meta.Item
	for _, it := range m.files {
		if it.Dirty {
			out = append(out, it)
		}
	}
	return out, nil
}

func (m *memRecords) Pieces(ino meta.Ino) ([]meta.Piece, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.pieces[ino]), nil
}

func (m *memRecords) AddPiece(ino meta.Ino, off, n int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if slices.ContainsFunc(m.pieces[ino], func(p meta.Piece) bool { return p.Off == off }) {
		return fmt.Errorf("a piece of %d at %d is recorded already", ino, off)
	}
	ps := slices.Concat(m.pieces[ino], []meta.Piece{{Off: off, Len: n}})
	slices.SortFunc(ps, func(a, b meta.Piece) int { return int(a.Off - b.Off) })
	m.pieces[ino] = ps
	return nil
}

func (m *memRecords) ReplacePieces(ino meta.Ino, pieces []meta.Piece) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	ps := slices.Clone(pieces)
	slices.SortFunc(ps, func(a, b meta.Piece) int { return int(a.Off - b.Off) })
	m.pieces[ino] = ps
	return nil
}

func (m *memRecords) DeletePiece(ino meta.Ino, off int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pieces[ino] = slices.DeleteFunc(m.pieces[ino], func(p meta.Piece) bool { return p.Off == off })
	return nil
}

func (m *memRecords) DeletePieces(ino meta.Ino) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.pieces, ino)
	return nil
}

func (m *memRecords) CachedPieces() ([]meta.CachedPiece, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []meta.CachedPiece
	for _, it := range m.files {
		if it.Dirty {
			continue
		}
		for _, p := range m.pieces[it.Ino] {
			out = append(out, meta.CachedPiece{Ino: it.Ino, ID: it.ID, Off: p.Off, Len: p.Len})
		}
	}
	return out, nil
}

func TestRoomAtAStartComesFromTheRecords(t *testing.T) {
	rec := newMemRecords(
		meta.Item{Ino: 2, ID: "1clean", Kind: meta.KindFile, RExists: true},
		meta.Item{Ino: 3, ID: "1other", Kind: meta.KindFile, RExists: true},
		meta.Item{Ino: 4, ID: "1edited", Kind: meta.KindFile, RExists: true, Dirty: true, Size: 700},
	)
	for _, p := range []struct {
		ino    meta.Ino
		off, n int64
	}{{2, 0, 100}, {2, 8192, 4097}, {3, 0, 4096}, {4, 0, 50000}} {
		if err := rec.AddPiece(p.ino, p.off, p.n); err != nil {
			t.Fatal(err)
		}
	}
	c, err := New(Options{
		Dir: filepath.Join(t.TempDir(), "content"), Records: rec, Size: 1 << 30,
		Log: slog.New(slog.DiscardHandler), Clock: clock.Real{},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if got, want := c.Used(), int64(6*4096); got != want {
		t.Fatalf("the cache takes %d bytes at its start, want %d", got, want)
	}
}

func TestHeldCountsCleanPiecesAndLocalFiles(t *testing.T) {
	rec := newMemRecords(
		meta.Item{Ino: 2, ID: "1clean", Kind: meta.KindFile, RExists: true},
		meta.Item{Ino: 4, ID: "1edited", Kind: meta.KindFile, RExists: true, Dirty: true, Size: 700},
		meta.Item{Ino: 5, ID: "1trashed", Kind: meta.KindFile, RExists: true, Dirty: true, Trashed: true, Size: 30},
		meta.Item{Ino: 6, ID: "1lost", Kind: meta.KindFile, Dirty: true, Lost: true, Size: 5},
		meta.Item{Ino: 7, ID: "1lostgone", Kind: meta.KindFile, Dirty: true, Lost: true, Trashed: true, Size: 1000000},
		meta.Item{Ino: 8, ID: "1lock", Kind: meta.KindFile, Dirty: true, Service: true, Size: 2},
	)
	for _, p := range []struct {
		ino    meta.Ino
		off, n int64
	}{{2, 0, 100}, {2, 8192, 4097}, {4, 0, 50000}} {
		if err := rec.AddPiece(p.ino, p.off, p.n); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Held(rec)
	if want := int64(100 + 4097 + 700 + 30 + 5 + 2); err != nil || got != want {
		t.Fatalf("the cache holds %d bytes, want %d: %v", got, want, err)
	}
}
