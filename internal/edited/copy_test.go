package edited

import (
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/cache"
	"github.com/Ruslan-Linuxoid/FastGD/internal/clock"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func TestDropBaseDuringATruncationLeavesNoBase(t *testing.T) {
	store, c := newCopyOverAStore(t)
	const id, size = "file-1", 3 << 20
	if err := store.PutItems([]meta.Item{{ID: id, Kind: meta.KindFile, Name: "f", Parent: "root",
		Size: size}}); err != nil {
		t.Fatal(err)
	}
	it, err := store.ByID(id)
	if err != nil {
		t.Fatal(err)
	}
	tail, err := c.makeLocal(it.Ino, id, size)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDirty(it.Ino, size, tail); err != nil {
		t.Fatal(err)
	}

	truncateWithDropBaseOnItsSync(t, c, it.Ino, id)
	now, err := store.ByID(id)
	if err != nil {
		t.Fatal(err)
	}
	if now.Base != 0 || now.BaseTail {
		t.Fatalf("the database holds a base of %d bytes, tail missing %v, after the whole file went up", now.Base,
			now.BaseTail)
	}
	if lacks, err := c.LacksBase(t.Context(), Local{Ino: it.Ino, ID: id}); err != nil || lacks {
		t.Fatalf("the working copy still wants bytes of the old version: %v", err)
	}
}

func newCopyOverAStore(t *testing.T) (*meta.Store, *Files) {
	t.Helper()
	store, err := meta.Open(filepath.Join(t.TempDir(), "meta.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	clean, err := cache.New(cache.Options{Dir: t.TempDir(), Records: store, Size: 1 << 30,
		Log: slog.New(slog.DiscardHandler), Clock: clock.Real{}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { clean.Close() })
	return store, New(clean, store, nil)
}

func truncateWithDropBaseOnItsSync(t *testing.T, c *Files, ino meta.Ino, id string) {
	t.Helper()

	atSync, goOn := make(chan struct{}), make(chan struct{})
	c.syncFile = func(fd *os.File) error {
		close(atSync)
		<-goOn
		return fd.Sync()
	}
	done := make(chan error, 2)
	go func() { done <- c.truncateLocal(ino, id, 1000) }()
	<-atSync
	go func() { done <- c.DropBase(ino) }()

	time.Sleep(100 * time.Millisecond)
	close(goOn)
	for range 2 {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
}
