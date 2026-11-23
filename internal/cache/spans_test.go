package cache

import (
	"slices"
	"testing"
)

func TestGaps(t *testing.T) {
	ss := spans{{10, 20}, {30, 40}, {40, 50}}
	cases := []struct {
		want span
		gaps []span
	}{
		{span{0, 10}, []span{{0, 10}}},
		{span{0, 60}, []span{{0, 10}, {20, 30}, {50, 60}}},
		{span{12, 18}, nil},
		{span{15, 35}, []span{{20, 30}}},
		{span{30, 50}, nil},
		{span{45, 55}, []span{{50, 55}}},
	}
	for _, c := range cases {
		if got := ss.gaps(c.want); !slices.Equal(got, c.gaps) {
			t.Errorf("gaps(%v) = %v, want %v", c.want, got, c.gaps)
		}
	}
	if got := (spans{}).gaps(span{0, 5}); !slices.Equal(got, []span{{0, 5}}) {
		t.Errorf("gaps of nothing = %v", got)
	}
}

func TestAddAndJoin(t *testing.T) {
	var ss spans
	for _, s := range []span{{40, 50}, {0, 10}, {10, 20}, {30, 40}} {
		ss.add(s)
	}
	if !slices.Equal(ss, spans{{0, 10}, {10, 20}, {30, 40}, {40, 50}}) {
		t.Fatalf("add keeps %v", ss)
	}
	if got := ss.joined(); !slices.Equal(got, spans{{0, 20}, {30, 50}}) {
		t.Fatalf("joined = %v", got)
	}
}

func TestPageSpan(t *testing.T) {
	if got := pageSpan(5000, 5001, 1<<20); got != (span{4096, 8192}) {
		t.Errorf("pageSpan = %v", got)
	}
	if got := pageSpan(5000, 9000, 6000); got != (span{4096, 6000}) {
		t.Errorf("pageSpan at the end of the file = %v", got)
	}
}

func TestWindowCapIsBoundedByTheCacheShare(t *testing.T) {
	const mib = 1 << 20
	if got := windowCap(4*mib, 1<<40, 1.0/8); got != 4*mib {
		t.Errorf("cap under the cache share = %d, want the amount", got)
	}
	if got := windowCap(512*mib, 64*mib, 1.0/8); got != 8*mib {
		t.Errorf("cap bounded by the cache = %d, want an eighth", got)
	}
}

func TestWindowOfNoSeriesIsNone(t *testing.T) {
	var w window
	for _, r := range []span{{0, 4096}, {1 << 20, 1<<20 + 4096}, {300, 900}, {8 << 20, 9 << 20}, {0, 4096}} {
		if got := w.plan(r.off, r.end, true, 1<<30); got.len() != 0 {
			t.Fatalf("the read %v prefetches %v", r, got)
		}
	}
}

func TestWindowOfASeriesForwardGrowsQuadratically(t *testing.T) {
	var w window
	const read, limit = 64 << 10, 4 << 20
	w.plan(0, read, true, limit)
	for n := int64(1); n <= 8; n++ {
		w.plan(n*read, (n+1)*read, false, limit)
		if want := min(read*n*n, limit); w.size != want {
			t.Fatalf("after %d reads of the series the window is %d, want %d", n, w.size, want)
		}
	}
}

func TestWindowOfASeriesBackwardGoesBefore(t *testing.T) {
	var w window
	const read = 64 << 10
	w.plan(5*read, 6*read, true, 1<<30)
	if got := w.plan(4*read, 5*read, true, 1<<30); got != (span{3 * read, 4 * read}) {
		t.Fatalf("the second read backward prefetches %v", got)
	}
	got := w.plan(3*read, 4*read, false, 1<<30)
	if got.off != 0 || got.end != 3*read {
		t.Fatalf("the third read backward prefetches %v, want the bytes before it cut at the start", got)
	}
}

func TestWindowAtTheCapGoesOutOncePerHalfWindow(t *testing.T) {
	var w window
	const limit, read = 1 << 20, 64 << 10
	windows := 0
	for off := int64(0); off < 64<<20; off += read {
		s := w.plan(off, off+read, true, limit)
		if s.len() > limit {
			t.Fatalf("a window of %d past the cap %d", s.len(), limit)
		}
		if s.len() > 0 {
			windows++
		}
	}
	if windows > 64+5 {
		t.Fatalf("a sequential read of 64 MiB sent %d windows", windows)
	}
}

func TestWindowTakesReadsOutOfOrder(t *testing.T) {
	var w window
	const limit = 1 << 20
	w.plan(0, 128<<10, true, limit)
	w.plan(128<<10, 256<<10, true, limit)

	w.plan(320<<10, 384<<10, true, limit)
	n := w.n
	if n != 2 {
		t.Fatalf("a read within the planned bytes took the series to %d, want 2", n)
	}
	w.plan(256<<10, 320<<10, true, limit)
	if w.n != n {
		t.Fatalf("a read the kernel handed over late took the series from %d to %d", n, w.n)
	}

	w.plan(4<<20, 4<<20+64<<10, true, limit)
	if w.n != 0 {
		t.Fatalf("a jump past the plan keeps the series at %d", w.n)
	}
}

func TestWindowOfALongSeriesStaysAtTheCap(t *testing.T) {
	if got := grown(1<<40, 1<<20, 1<<30); got != 1<<30 {
		t.Fatalf("a long series grows a window of %d, want the cap", got)
	}
	if got := grown(64<<10, 3, 1<<30); got != 9*64<<10 {
		t.Fatalf("a series of three steps of 64 KiB grows a window of %d", got)
	}
}

func TestWindowShrinksToALowerCap(t *testing.T) {
	var w window
	for off := int64(0); w.size < 1<<20; off += 64 << 10 {
		w.plan(off, off+64<<10, true, 1<<20)
	}
	if w.plan(w.far, w.far+64<<10, false, 256<<10); w.size != 256<<10 {
		t.Fatalf("after the cap fell to 256 KiB the window is %d", w.size)
	}
}

func TestSharedCapDividesTheWindowAmongTheFiles(t *testing.T) {
	for _, c := range []struct {
		limit int64
		files int
		want  int64
	}{
		{4 << 20, 1, 4 << 20},
		{4 << 20, 2, 2 << 20},

		{4 << 20, 3, 1396736},
		{3 * page(), 2, page()},

		{2 * page(), 5, page()},
	} {
		if got := sharedCap(c.limit, c.files); got != c.want {
			t.Errorf("%d bytes among %d files give %d, want %d", c.limit, c.files, got, c.want)
		}
	}
}

func TestPortionIsWholeUnitsAndNoLessThan256KiB(t *testing.T) {
	t.Parallel()
	const kib = 1 << 10
	for unit, want := range map[int64]int64{
		4 * kib: 256 * kib, 64 * kib: 256 * kib, 128 * kib: 256 * kib, 256 * kib: 256 * kib,
		96 * kib: 288 * kib, 384 * kib: 384 * kib, 1024 * kib: 1024 * kib, 16 * 1024 * kib: 16 * 1024 * kib,
	} {
		if got := portionOf(unit); got != want {
			t.Errorf("with units of %d bytes the portion is %d, want %d", unit, got, want)
		}
	}
}
