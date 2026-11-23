package core_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestTheQuotaOfUnitsHoldsWithTheRate(t *testing.T) {
	inBubble(t, drive.Options{Limits: drive.Limits{Units: 4000, UnitsWindow: time.Second, RetryFor: time.Minute}},
		func(t *testing.T, emu *emulator.Emulator, cl *drive.Client) {
			emu.SetRateQuota(4000, time.Second)
			content := testenv.RandomBytes(4<<10, 71)
			id := emu.WriteFile(emu.Root(), "f.bin", content)
			emu.ResetRequests()
			var wg sync.WaitGroup
			for range 60 {
				wg.Go(func() {
					sink := func([]byte) error { return nil }
					if err := cl.Stream(context.Background(), id, 0, int64(len(content)), sink); err != nil {
						t.Error(err)
					}
				})
				wg.Go(func() {
					if _, err := cl.Get(context.Background(), id); err != nil {
						t.Error(err)
					}
				})
			}
			wg.Wait()
			if n := emu.RateRefusals(); n != 0 {
				t.Fatalf("Drive refused %d calls by the quota of units", n)
			}
			if r, _ := emu.RatePeaks(); r > 30 {
				t.Fatalf("Drive got up to %d reads in a second", r)
			}
		})
}

func requireRateHalvesOnARefusalOfAGet(t *testing.T, emu *emulator.Emulator, cl *drive.Client) {
	t.Helper()
	ctx := drive.InBackground(context.Background())
	root := emu.Root()

	emu.FailRequests(1, 429, "rateLimitExceeded", 0)
	if _, err := cl.Get(ctx, root); err != nil {
		t.Fatal(err)
	}
	time.Sleep(2 * time.Second)
	emu.ResetRequests()
	readsAndWrites(t, cl, root, 60)
	if r, w := emu.RatePeaks(); r != 15 || w != 30 {
		t.Fatalf("after one refusal of a read Drive got up to %d reads and %d writes in a second, want 15 and 30", r, w)
	}
}

func requireRateOfWritesFallsOnRefusals(t *testing.T, emu *emulator.Emulator, cl *drive.Client) {
	t.Helper()
	root := emu.Root()

	time.Sleep(time.Minute)
	emu.FailRequests(1<<20, 403, "userRateLimitExceeded", 0)
	until := time.Now().Add(12 * time.Second)
	lift := time.AfterFunc(12*time.Second, func() { emu.FailRequests(0, 0, "", 0) })
	t.Cleanup(func() { lift.Stop() })
	var refused sync.WaitGroup
	for range 200 {
		refused.Go(func() {
			for time.Now().Before(until) {
				_, _ = cl.GenerateIDs(context.Background(), 1)
			}
		})
	}
	refused.Wait()
	time.Sleep(2 * time.Second)
	emu.ResetRequests()
	readsAndWrites(t, cl, root, 12)
	if r, w := emu.RatePeaks(); r != 12 || w < 3 || w > 4 {
		t.Fatalf("after refusals of writes for 12 s Drive got up to %d reads and %d writes in a second, want 12 and 3 or 4",
			r, w)
	}
}

func refuseEveryReadForAMinute(t *testing.T, emu *emulator.Emulator, cl *drive.Client) {
	t.Helper()
	root := emu.Root()

	emu.FailRequests(1<<20, 429, "rateLimitExceeded", 0)
	stop := time.Now().Add(time.Minute)
	for time.Now().Before(stop) {
		_, _ = cl.Get(context.Background(), root)
	}
}

func requireRateGrowsBack(t *testing.T, emu *emulator.Emulator, cl *drive.Client) {
	t.Helper()
	ctx := drive.InBackground(context.Background())
	root := emu.Root()
	emu.FailRequests(0, 0, "", 0)
	emu.ResetRequests()
	done := make(chan struct{})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for {
				select {
				case <-done:
					return
				default:
					_, _ = cl.Get(ctx, root)
				}
			}
		})
	}

	time.Sleep(11 * time.Second)
	if n := len(emu.Requests()); n < 1 || n > 3 {
		t.Fatalf("at the lowest rate Drive got %d reads in 11 s, want 1 to 3", n)
	}

	time.Sleep(20 * time.Minute)
	emu.ResetRequests()
	time.Sleep(3 * time.Second)
	if r, _ := emu.RatePeaks(); r < 15 || r > 22 {
		t.Fatalf("twenty minutes after the refusals Drive gets up to %d reads in a second, want about 20", r)
	}
	time.Sleep(11 * time.Minute)
	emu.ResetRequests()
	time.Sleep(3 * time.Second)
	close(done)
	wg.Wait()
	if r, _ := emu.RatePeaks(); r != 30 {
		t.Fatalf("half an hour after the refusals Drive gets up to %d reads in a second, want 30", r)
	}
}

func TestRateFallsOnRefusalsAndGrowsBack(t *testing.T) {
	inBubble(t, drive.Options{Limits: drive.DefaultLimits()}, func(t *testing.T, emu *emulator.Emulator,
		cl *drive.Client) {
		requireRateHalvesOnARefusalOfAGet(t, emu, cl)
		requireRateOfWritesFallsOnRefusals(t, emu, cl)
		refuseEveryReadForAMinute(t, emu, cl)
		requireRateGrowsBack(t, emu, cl)
	})
}
