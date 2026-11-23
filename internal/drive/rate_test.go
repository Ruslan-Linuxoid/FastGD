package drive

import (
	"context"
	"testing"
	"testing/synctest"
	"time"
)

func TestCallRateFallsOnceInFiveSecondsDownToATenth(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := &callRate{top: perSecond, per: perSecond}

		for range 100 {
			r.refused()
			time.Sleep(10 * time.Millisecond)
		}
		if want := float64(perSecond) / 2; r.per != want {
			t.Fatalf("after a second of refusals the rate is %v a second, want %v", r.per, want)
		}

		for range 1600 {
			r.refused()
			time.Sleep(10 * time.Millisecond)
		}
		if want := float64(perSecond) / 16; r.per != want {
			t.Fatalf("after 17 s of refusals the rate is %v a second, want %v", r.per, want)
		}
		for range 60 {
			r.refused()
			time.Sleep(5 * time.Second)
		}
		if r.per != leastPerSecond {
			t.Fatalf("after five minutes of refusals the rate is %v a second, want %v", r.per, leastPerSecond)
		}

		ctx := context.Background()
		r.refused()
		if err := r.take(ctx); err != nil {
			t.Fatal(err)
		}
		start := time.Now()

		time.Sleep(6 * time.Second)
		r.refused()
		if err := r.take(ctx); err != nil {
			t.Fatal(err)
		}
		if took := time.Since(start); took != 10200*time.Millisecond {
			t.Fatalf("at the lowest rate the next call went after %v, want 10.2 s", took)
		}
	})
}

func TestCallRateGrowsBackByOneAMinute(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx := context.Background()
		r := &callRate{top: perSecond, per: perSecond}
		r.refused()
		time.Sleep(59 * time.Second)
		requireRateAfterTake(ctx, t, r, perSecond/2, "59 s after a refusal")

		time.Sleep(3*time.Minute + 2*time.Second)
		requireRateAfterTake(ctx, t, r, perSecond/2+4, "four minutes after a refusal")

		time.Sleep(30 * time.Second)
		r.refused()
		cut := r.per
		time.Sleep(59 * time.Second)
		requireRateAfterTake(ctx, t, r, cut, "59 s after a refusal")
		time.Sleep(2 * time.Second)
		requireRateAfterTake(ctx, t, r, cut+1, "61 s after a refusal")

		low := &callRate{top: perSecond, per: leastPerSecond}
		low.refused()
		time.Sleep(29 * time.Minute)
		if err := low.take(ctx); err != nil || low.per >= perSecond {
			t.Fatalf("29 minutes after the last refusal the rate is back at %v a second: %v", low.per, err)
		}
		time.Sleep(90 * time.Second)
		requireRateAfterTake(ctx, t, low, perSecond, "half an hour after the last refusal")
	})
}

func requireRateAfterTake(ctx context.Context, t *testing.T, r *callRate, want float64, when string) {
	t.Helper()
	if err := r.take(ctx); err != nil || r.per != want {
		t.Fatalf("%s the rate is %v a second, want %v: %v", when, r.per, want, err)
	}
}

func TestTakingTheReconnectFunctionAwayWaitsForItsRun(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		c := &Client{pace: newPacer(DefaultLimits())}
		running, done := make(chan struct{}), false
		c.OnReconnect(func() {
			close(running)
			time.Sleep(time.Minute)
			done = true
		})
		c.pace.note(offline)
		c.pace.note(passed)
		<-running
		c.OnReconnect(nil)
		if !done {
			t.Fatal("the function of a reconnect was still running when OnReconnect(nil) returned")
		}

		c.pace.note(offline)
		c.pace.note(passed)
	})
}
