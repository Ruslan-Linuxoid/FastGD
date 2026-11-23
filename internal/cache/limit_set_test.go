package cache

import (
	"errors"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/clock"
)

type toldLimits struct {
	mu   sync.Mutex
	took []int64
	fail bool
}

func (l *toldLimits) changed(limit int64, _ string) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.took = append(l.took, limit)
	if l.fail {
		return errors.New("the disk is full")
	}
	return nil
}

func limitCache(t *testing.T, told *toldLimits) *Cache {
	t.Helper()
	c, err := New(Options{
		Dir: filepath.Join(t.TempDir(), "content"), Records: newMemRecords(), Size: 1 << 30,
		Log: slog.New(slog.DiscardHandler), Clock: clock.NewFake(time.Unix(0, 0)), LimitChanged: told.changed,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	c.takeLimit(1000000, 0, "cache_size")
	told.took = nil
	return c
}

func TestALimitIsToldWhenItMovesByAHundredth(t *testing.T) {
	t.Parallel()
	told := &toldLimits{}
	c := limitCache(t, told)
	c.takeLimit(1005000, 0, "cache_size")
	if len(told.took) != 0 {
		t.Fatalf("a limit that moved by less than a hundredth was told: %v", told.took)
	}
	c.takeLimit(1020000, 0, "cache_size")
	if len(told.took) != 1 || told.took[0] != 1020000 {
		t.Fatalf("a limit that moved by two hundredths with the same cause was told as %v", told.took)
	}
}

func TestALimitNotTakenIsToldAgain(t *testing.T) {
	t.Parallel()
	told := &toldLimits{}
	c := limitCache(t, told)
	told.fail = true
	c.takeLimit(1020000, 0, "cache_size")
	told.fail = false
	c.takeLimit(1020000, 0, "cache_size")
	c.takeLimit(1020000, 0, "cache_size")
	if len(told.took) != 2 {
		t.Fatalf("the limit went to LimitChanged %d times, want twice: once failed, once taken", len(told.took))
	}
}
