package cache

import "testing"

func TestWindowTurnOfALongSeriesStartsItAnew(t *testing.T) {
	t.Parallel()
	var w window
	const read, limit = 64 << 10, 1 << 30
	for i := range int64(4) {
		w.plan(10*read+i*read, 11*read+i*read, true, limit)
	}
	if w.n != 3 || w.size != 9*read {
		t.Fatalf("scenario not built: the series forward is %d long, its window %d", w.n, w.size)
	}
	w.plan(3*read, 13*read, true, limit)
	if w.n != 1 || w.dir != -1 || w.size != 10*read {
		t.Fatalf("the turn left a series of %d in the direction %d with a window of %d, want 1, -1 and %d",
			w.n, w.dir, w.size, 10*read)
	}
}

func TestWindowReadAtTheEndOfThePlanEndsTheSeries(t *testing.T) {
	t.Parallel()
	var w window
	const read, limit = 64 << 10, 1 << 30
	for i := range int64(3) {
		w.plan(i*read, (i+1)*read, true, limit)
	}
	if w.n != 2 || w.ahead <= w.far {
		t.Fatalf("scenario not built: a series of %d planned up to %d past the reads at %d", w.n, w.ahead, w.far)
	}
	w.plan(w.ahead, w.ahead+read, true, limit)
	if w.n != 0 {
		t.Fatalf("a read at the end of the plan kept the series at %d", w.n)
	}
}

func TestWindowGrowsByTheMeanStep(t *testing.T) {
	t.Parallel()
	var w window
	const limit = 1 << 40
	w.plan(0, 4<<10, true, limit)
	w.plan(4<<10, 8<<10, true, limit)
	w.plan(8<<10, 8<<10+1<<20, true, limit)
	if want := PageUp((4<<10 + 1<<20) / 2 * 4); w.size != want {
		t.Fatalf("after reads of 4 KiB, 4 KiB and 1 MiB the window is %d, want %d", w.size, want)
	}
}

func TestWindowJumpWithinThePlanGrowsByTheReadAlone(t *testing.T) {
	t.Parallel()
	var w window
	const read, limit = 128 << 10, 32 << 20
	for i := range int64(4) {
		w.plan(i*read, (i+1)*read, true, limit)
	}
	if w.n != 3 || w.ahead < w.far+2*read {
		t.Fatalf("scenario not built: a series of %d planned up to %d past the reads at %d", w.n, w.ahead, w.far)
	}
	w.plan(w.ahead-read, w.ahead, true, limit)
	if w.n != 4 || w.size != 16*read {
		t.Fatalf("the jump within the plan left a series of %d with a window of %d, want 4 and %d", w.n, w.size, 16*read)
	}
}
