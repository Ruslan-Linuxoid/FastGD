package cache

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/clock"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func TestNewRefusesAFileSystemWithoutHoles(t *testing.T) {
	_, err := New(Options{
		Dir: t.TempDir(), Records: newMemRecords(), Size: 1 << 30,
		punchHoleAt: func(int, int64, int64) error { return syscall.EOPNOTSUPP },
	})
	if err == nil || !strings.Contains(err.Error(), "cannot cut holes") {
		t.Fatalf("a file system that cannot cut holes got %v", err)
	}
}

func TestJoinedPiecesKeepTheirHits(t *testing.T) {
	w := worths{of: map[meta.Ino]map[int64]*worth{}, gone: map[pieceAt]int64{}, local: map[meta.Ino]int64{}}
	w.known(7, 0, 2).floor = 1
	w.known(7, 4096, 3).floor = 5
	w.known(7, 8192, 1)
	w.known(7, 1<<20, 4)
	w.known(8, 0, 9)
	w.joined(7, spans{{0, 12288}, {1 << 20, 1<<20 + 4096}})
	if got := w.of[7]; len(got) != 2 || got[0].hits != 6 || got[0].floor != 5 || got[1<<20].hits != 4 {
		t.Fatalf("after the join the file has %d pieces, the first %+v", len(got), got[0])
	}
	if w.of[8][0].hits != 9 {
		t.Fatal("the join of one file changed the hits of another")
	}
}

func TestAdoptedFileHasTheHitsOfItsEdits(t *testing.T) {
	w := worths{of: map[meta.Ino]map[int64]*worth{}, gone: map[pieceAt]int64{}, local: map[meta.Ino]int64{7: 5}}
	w.known(7, 4096, 3)
	w.adopted(7, 4096)
	if got := w.of[7]; len(got) != 1 || got[0].hits != 5 || len(w.local) != 0 {
		t.Fatalf("the adopted file has the pieces %v, and %d files counted as changed here", got, len(w.local))
	}
}

func TestTheReadAPieceCameForIsNotCountedTwice(t *testing.T) {
	w := worths{of: map[meta.Ino]map[int64]*worth{}, gone: map[pieceAt]int64{}, local: map[meta.Ino]int64{}}
	w.came(7, span{0, 4096}, 0)
	w.hit(7, 0)
	w.came(7, span{4096, 8192}, 1)
	if a, b := w.of[7][0].hits, w.of[7][4096].hits; a != 1 || b != 1 {
		t.Fatalf("a piece fetched ahead and read has %d hits, one read as it came %d, want 1 and 1", a, b)
	}
	w.hit(7, 0)
	w.hit(7, 4096)
	if a, b := w.of[7][0].hits, w.of[7][4096].hits; a != 2 || b != 2 {
		t.Fatalf("read once more, the pieces have %d and %d hits, want 2 and 2", a, b)
	}
}

func TestReadsFromTheDownloadAreHitsOfThePiece(t *testing.T) {
	w := worths{of: map[meta.Ino]map[int64]*worth{}, gone: map[pieceAt]int64{}, local: map[meta.Ino]int64{}}
	w.came(7, span{0, 4096}, 3)
	if got := w.of[7][0].hits; got != 3 {
		t.Fatalf("a piece three reads took from its download comes with %d hits, want 3", got)
	}
	w.hit(7, 0)
	if got := w.of[7][0].hits; got != 4 {
		t.Fatalf("a read of the recorded piece makes %d hits, want 4", got)
	}
}

func TestNoIDLeadsOutOfTheCache(t *testing.T) {
	root := t.TempDir()
	c, err := New(Options{
		Dir: filepath.Join(root, "content"), Records: newMemRecords(), Size: 1 << 30,
		Log: slog.New(slog.DiscardHandler), Clock: clock.Real{},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	victim := filepath.Join(root, "victim")
	if err := os.WriteFile(victim, []byte("not the cache's"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := c.CreateLocal("1abc"); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"ab/../../victim", "../victim", "..", ".", "", "a\x00b", `a\b`} {
		if err := c.LinkLocal("1abc", id); err == nil {
			t.Errorf("the content got the name of %q", id)
		}
		_ = c.UnlinkLocal(id)
		_ = c.MoveLocal("1abc", id)
		if got, err := os.ReadFile(victim); err != nil || string(got) != "not the cache's" {
			t.Fatalf("after the id %q the file beside the cache holds %q: %v", id, got, err)
		}
		if _, err := c.LocalSize("1abc"); err != nil {
			t.Fatalf("after the id %q the content of the file is gone: %v", id, err)
		}
	}
}

func TestUnitsOfHolesTheCacheCountsBy(t *testing.T) {
	t.Parallel()
	const kib = 1 << 10
	for unit, want := range map[int64]bool{
		4 * kib: true, 64 * kib: true, 384 * kib: true, 1024 * kib: true, 16 * 1024 * kib: true,
		2 * kib: false, 6000: false, 32 * 1024 * kib: false,
	} {
		if got := countable(unit); got != want {
			t.Errorf("counting by units of %d bytes: %v, want %v", unit, got, want)
		}
	}
}
