package cache

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func TestPrefetchGivesItsSlotBackBeforeEachWait(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		const size = 64 << 10
		busy := errors.New("busy")
		var tries atomic.Int32
		c, err := New(Options{
			Dir: t.TempDir(), Records: newMemRecords(meta.Item{Ino: 2, ID: "f", Size: size}), Size: 1 << 30,
			Source: func(_ context.Context, _, _ string, _, n int64, sink func([]byte) error) error {
				if tries.Add(1) <= 2 {
					return busy
				}
				return sink(make([]byte, n))
			},
			Passing: func(err error) bool { return errors.Is(err, busy) },
			Log:     slog.New(slog.DiscardHandler),
		})
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		f := File{Ino: 2, ID: "f", Size: size, Base: size}
		r, err := c.Open(f)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		c.prefetch(r.g, f, span{0, size})
		time.Sleep(time.Minute)
		synctest.Wait()
		if n := tries.Load(); n != 3 {
			t.Fatalf("scenario not built: the prefetch went %d times, want two failures and a third that comes", n)
		}
		if n := len(c.sem); n != 0 {
			t.Fatalf("after the prefetch %d prefetch slots stay taken", n)
		}
	})
}
