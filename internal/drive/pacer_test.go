package drive

import (
	"testing"
	"testing/synctest"
	"time"
)

func TestCallGivenBackAfterItLeftTheWindowCountsOnce(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		p := newPacer(Limits{Units: 1000, UnitsWindow: time.Second})
		long, _ := p.tryTake(100)
		if long == nil || p.used != 100 {
			t.Fatalf("scenario not built: %d units are spent", p.used)
		}
		time.Sleep(3 * time.Second)

		if s, _ := p.tryTake(10); s == nil || p.used != 10 {
			t.Fatalf("after the window %d units are spent, want the 10 of the new call", p.used)
		}
		p.giveBack(long)
		if p.used != 10 {
			t.Fatalf("giving back a call that left the window leaves %d units spent, want 10", p.used)
		}
	})
}

func TestLimitsNotSetTakeTheDefaults(t *testing.T) {
	t.Parallel()
	p := newPacer(Limits{Units: 77})
	if p.lim.Units != 77 || p.lim.UnitsWindow != DefaultLimits().UnitsWindow ||
		p.lim.RetryFor != DefaultLimits().RetryFor ||
		p.lim.IdleRead != DefaultLimits().IdleRead {
		t.Fatalf("limits with the units alone set are %+v", p.lim)
	}
	if p := newPacer(Limits{UnitsWindow: time.Minute}); p.lim.Units != DefaultLimits().Units ||
		p.lim.UnitsWindow != time.Minute {
		t.Fatalf("limits with the window alone set are %+v", p.lim)
	}
}
