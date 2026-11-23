package core_test

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func inBubble(t *testing.T, o drive.Options, test func(t *testing.T, emu *emulator.Emulator, cl *drive.Client)) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		emu := emulator.New()
		o.BaseURL, o.HTTPClient = "http://emulator", &http.Client{Transport: emulator.InProcess(emu)}
		cl, err := drive.New(context.Background(), o)
		if err != nil {
			t.Fatal(err)
		}
		test(t, emu, cl)
	})
}

func load(cl *drive.Client, root string, stop <-chan struct{}) (*atomic.Int64, func()) {
	var done atomic.Int64
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := cl.Get(context.Background(), root); err == nil {
					done.Add(1)
				}
			}
		})
	}
	return &done, wg.Wait
}

func TestCallsStayUnderTheQuota(t *testing.T) {
	inBubble(t, drive.Options{Limits: drive.Limits{Units: 2000, UnitsWindow: time.Second, RetryFor: 5 * time.Second}},
		func(t *testing.T, emu *emulator.Emulator, cl *drive.Client) {
			emu.SetRateQuota(2000, time.Second)
			content := testenv.RandomBytes(4<<10, 70)
			id := emu.WriteFile(emu.Root(), "f.bin", content)
			var wg sync.WaitGroup
			for range 20 {
				wg.Go(func() {
					sink := func([]byte) error { return nil }
					if err := cl.Stream(context.Background(), id, 0, int64(len(content)), sink); err != nil {
						t.Error(err)
					}
					if _, err := cl.Get(context.Background(), id); err != nil {
						t.Error(err)
					}
				})
			}
			wg.Wait()
			if n := emu.RateRefusals(); n != 0 {
				t.Fatalf("Drive refused %d calls by the quota", n)
			}
		})
}

func TestPaceFollowsACutQuotaAndGrowsBack(t *testing.T) {

	inBubble(t, drive.Options{Limits: drive.Limits{Units: 4000, UnitsWindow: time.Second, RetryFor: 10 * time.Second,
		PerSecond: 1e6}}, func(t *testing.T, emu *emulator.Emulator, cl *drive.Client) {
		emu.SetRateQuota(1000, time.Second)
		stop := make(chan struct{})
		done, ended := load(cl, emu.Root(), stop)
		defer ended()
		defer close(stop)

		time.Sleep(2 * time.Second)
		refused := emu.RateRefusals()
		if refused == 0 {
			t.Fatal("the cut quota refused nothing: the test does not load enough")
		}
		start := done.Load()
		time.Sleep(3 * time.Second)
		if n := emu.RateRefusals() - refused; n != 0 {
			t.Fatalf("after the pace followed the cut quota Drive still refused %d calls", n)
		}
		cut := done.Load() - start

		emu.SetRateQuota(0, 0)
		time.Sleep(3 * time.Second)
		start = done.Load()
		time.Sleep(3 * time.Second)
		if back := done.Load() - start; back <= cut*11/10 {
			t.Fatalf("with the quota back the pace went from %d to %d calls in 3 s", cut, back)
		}
	})
}

func TestRetryAfterIsKept(t *testing.T) {
	inBubble(t, drive.Options{Limits: drive.Limits{Units: 325_000, UnitsWindow: time.Minute, RetryFor: 5 * time.Second}},
		func(t *testing.T, emu *emulator.Emulator, cl *drive.Client) {
			emu.FailRequests(1, 429, "rateLimitExceeded", time.Second)
			start := time.Now()
			if _, err := cl.Get(context.Background(), emu.Root()); err != nil {
				t.Fatal(err)
			}
			if took := time.Since(start); took < time.Second {
				t.Fatalf("a call Drive asked to wait a second for went again after %v", took)
			}
		})
}

func readsAndWrites(t *testing.T, cl *drive.Client, root string, n int) {
	t.Helper()
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() {
			if _, err := cl.Get(drive.InBackground(context.Background()), root); err != nil {
				t.Error(err)
			}
		})
		wg.Go(func() {
			if _, err := cl.GenerateIDs(drive.InBackground(context.Background()), 1); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
}

func TestThirtyReadsAndThirtyWritesASecond(t *testing.T) {
	inBubble(t, drive.Options{Limits: drive.DefaultLimits()}, func(t *testing.T, emu *emulator.Emulator,
		cl *drive.Client) {
		emu.ResetRequests()
		start := time.Now()
		readsAndWrites(t, cl, emu.Root(), 100)

		if took := time.Since(start); took < 3*time.Second || took > 4*time.Second {
			t.Fatalf("100 reads and 100 writes took %v, want a little over 3 s", took)
		}
		if r, w := emu.RatePeaks(); r != 30 || w != 30 {
			t.Fatalf("Drive got up to %d reads and %d writes in a second, want 30 and 30", r, w)
		}
	})
}
