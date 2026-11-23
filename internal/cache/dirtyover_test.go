package cache

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/clock"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

type gatedRecords struct {
	*memRecords

	mu      sync.Mutex
	gate    chan struct{}
	entered chan struct{}
}

func (g *gatedRecords) DirtyIDs() ([]string, error) {
	g.mu.Lock()
	gate, entered := g.gate, g.entered
	g.entered = nil
	g.mu.Unlock()
	if gate != nil {
		if entered != nil {
			close(entered)
		}
		<-gate
	}
	return g.memRecords.DirtyIDs()
}

func TestDirtyOverWaitsForTheCountUnderWay(t *testing.T) {
	t.Parallel()
	id := "dirty-file"
	rec := &gatedRecords{memRecords: newMemRecords(meta.Item{Ino: 2, ID: id, Dirty: true})}
	c, err := New(Options{
		Dir: filepath.Join(t.TempDir(), "content"), Records: rec, Size: 1 << 20,
		Log: slog.New(slog.DiscardHandler), Clock: clock.Real{},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if err := os.MkdirAll(filepath.Dir(c.path(id)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(c.path(id), bytes.Repeat([]byte{1}, 2<<20), 0o600); err != nil {
		t.Fatal(err)
	}
	gate, entered := make(chan struct{}), make(chan struct{})
	rec.mu.Lock()
	rec.gate, rec.entered = gate, entered
	rec.mu.Unlock()
	c.Changed()

	go c.refreshLimit(false)
	<-entered
	release := time.AfterFunc(100*time.Millisecond, func() {
		rec.mu.Lock()
		rec.gate = nil
		rec.mu.Unlock()
		close(gate)
	})
	defer release.Stop()
	if over := c.DirtyOver(); over <= 0 {
		t.Fatalf("2 MiB of dirty files are %d bytes over a limit of 1 MiB", over)
	}
}
