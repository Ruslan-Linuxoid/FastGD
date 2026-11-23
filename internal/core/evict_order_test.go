package core_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestLargePiecesReadOnceGoBeforeSmallOnesReadAgain(t *testing.T) {
	testenv.InBubble(t, func(t *testing.T, env *testenv.Env) {
		env.CacheSize = 2 * mib
		const size = 6 * mib
		content := testenv.RandomBytes(size, 260)
		env.Emu.WriteFile(env.Emu.Root(), "video", content)
		env.Start()
		for range 3 {
			readPart(t, env, "video", 0, 16*kib)
			readPart(t, env, "video", size-16*kib, 16*kib)
		}
		fh := openFile(t, env, "video")
		for off := int64(0); off < size; off += 128 * kib {
			mustRead(t, env, fh, off, 128*kib)
		}
		waitUntil(t, func() bool { return cached(t, env, "video") == size })
		env.FS.Release(fh)
		time.Sleep(evictionTime)
		requireLargestPiecesGone(t, env, size)
		requireReadsAfterEviction(t, env, content)
	})
}

func TestAnEvictedPieceComesBackWithItsHits(t *testing.T) {
	testenv.InBubble(t, func(t *testing.T, env *testenv.Env) {
		env.CacheSize = mib
		const size = 4 * mib
		env.Emu.WriteFile(env.Emu.Root(), "a", testenv.RandomBytes(size, 261))
		env.Emu.WriteFile(env.Emu.Root(), "b", testenv.RandomBytes(size, 262))
		env.Start()

		for range 6 {
			readPart(t, env, "a", 0, 400*kib)
		}
		for range 10 {
			readPart(t, env, "a", mib, 400*kib)
			readPart(t, env, "a", 2*mib, 400*kib)
		}
		time.Sleep(evictionTime)
		if holds(t, env, "a", 0, 400*kib) || !holds(t, env, "a", mib, 400*kib) || !holds(t, env, "a", 2*mib, 400*kib) {
			t.Fatalf("scenario not built: %v", pieces(t, env, "a"))
		}

		readPart(t, env, "a", 0, 400*kib)
		for range 3 {
			readPart(t, env, "b", 0, 400*kib)
		}
		time.Sleep(evictionTime)
		if !holds(t, env, "a", 0, 400*kib) {
			t.Fatalf("the piece downloaded again after six reads went as one read once: a %v, b %v", pieces(t, env, "a"),
				pieces(t, env, "b"))
		}
	})
}

func TestManySmallReadsOfAPieceAreOneHit(t *testing.T) {
	testenv.InBubble(t, func(t *testing.T, env *testenv.Env) {
		env.CacheSize = mib
		files(env, 3, 400*kib)
		env.Start()
		readClosed(t, env, "f0")
		fh := openFile(t, env, "f0")
		for off := int64(0); off < 400*kib; off += 4 * kib {
			mustRead(t, env, fh, off, 4*kib)
		}
		env.FS.Release(fh)
		for range 4 {
			readClosed(t, env, "f1")
		}
		readClosed(t, env, "f2")
		readClosed(t, env, "f2")
		readClosed(t, env, "f2")
		time.Sleep(evictionTime)
		if cached(t, env, "f0") != 0 || cached(t, env, "f1") != 400*kib || cached(t, env, "f2") != 400*kib {
			t.Fatalf("f0, read through twice, has %d bytes cached; f1, read four times, %d; f2, read three times, %d",
				cached(t, env, "f0"), cached(t, env, "f1"), cached(t, env, "f2"))
		}
	})
}

func TestSmallFilesOutliveLargeOnes(t *testing.T) {
	testenv.InBubble(t, func(t *testing.T, env *testenv.Env) {
		env.CacheSize = mib
		for i := range 3 {
			env.Emu.WriteFile(env.Emu.Root(), "small"+string(rune('0'+i)), testenv.RandomBytes(16*kib, uint64(240+i)))
		}
		env.Emu.WriteFile(env.Emu.Root(), "large", testenv.RandomBytes(800*kib, 243))
		env.Emu.WriteFile(env.Emu.Root(), "last", testenv.RandomBytes(300*kib, 244))
		env.Start()

		for i := range 3 {
			readClosed(t, env, "small"+string(rune('0'+i)))
		}
		readClosed(t, env, "large")
		readClosed(t, env, "last")
		time.Sleep(evictionTime)
		if cached(t, env, "large") != 0 {
			t.Fatal("the large file outlived the small ones")
		}
		for _, f := range []string{"small0", "small1", "small2", "last"} {
			if cached(t, env, f) == 0 {
				t.Fatalf("%s went before the large file", f)
			}
		}
	})
}

func TestOftenReadFilesOutliveIdleOnes(t *testing.T) {
	testenv.InBubble(t, func(t *testing.T, env *testenv.Env) {
		env.CacheSize = mib
		files(env, 3, 400*kib)
		env.Start()

		for range 6 {
			readClosed(t, env, "f0")
		}
		readClosed(t, env, "f1")
		readClosed(t, env, "f2")
		time.Sleep(evictionTime)
		if cached(t, env, "f0") != 400*kib || cached(t, env, "f1") != 0 {
			t.Fatalf("f0, read six times, has %d bytes cached, f1, read once since, %d", cached(t, env, "f0"), cached(t, env,
				"f1"))
		}
	})
}

func TestTheOrderOfEvictionStartsAnewAfterARestart(t *testing.T) {
	testenv.InBubble(t, func(t *testing.T, env *testenv.Env) {
		env.CacheSize = mib
		files(env, 3, 400*kib)
		env.Start()
		for range 6 {
			readClosed(t, env, "f0")
		}
		readClosed(t, env, "f1")
		env.Restart()
		readClosed(t, env, "f2")
		time.Sleep(evictionTime)

		if cached(t, env, "f0") != 0 || cached(t, env, "f1") != 400*kib {
			t.Fatalf("after a restart f0 has %d bytes cached, f1 %d: the order of the run before was kept", cached(t, env, "f0"),
				cached(t, env, "f1"))
		}
	})
}

func TestAnEvictedFileComesBackWithItsHits(t *testing.T) {
	testenv.InBubble(t, func(t *testing.T, env *testenv.Env) {
		env.CacheSize = mib
		files(env, 3, 400*kib)
		env.Start()
		for range 6 {
			readClosed(t, env, "f0")
		}

		for range 10 {
			readClosed(t, env, "f1")
			readClosed(t, env, "f2")
		}
		time.Sleep(evictionTime)
		if cached(t, env, "f0") != 0 {
			t.Fatal("scenario not built: f0 was not evicted")
		}

		readClosed(t, env, "f0")
		time.Sleep(evictionTime)
		if cached(t, env, "f0") != 400*kib {
			t.Fatal("f0, downloaded again after six reads, went as a file read once")
		}
	})
}

func requireLargestPiecesGone(t *testing.T, env *testenv.Env, size int64) {
	t.Helper()
	if !holds(t, env, "video", 0, 16*kib) || !holds(t, env, "video", size-16*kib, 16*kib) {
		t.Fatalf("the header or the tail, read four times, went before pieces read once: %v", pieces(t, env, "video"))
	}

	if n := du(t, env); n > 2*mib || n <= mib-4*kib {
		t.Fatalf("after the eviction the cache takes %d bytes, want just within 2 MiB", n)
	}
	if !sparse(t, env, "video") || holds(t, env, "video", 3*mib, mib) {
		t.Fatalf("the middle of the file is still cached, or its file is gone: %v", pieces(t, env, "video"))
	}
}

func requireReadsAfterEviction(t *testing.T, env *testenv.Env, content []byte) {
	t.Helper()

	env.Emu.ResetRequests()
	if got := readPart(t, env, "video", 0, 16*kib); !bytes.Equal(got, content[:16*kib]) {
		t.Fatal("the header reads different")
	}
	if got := mediaRanges(env); len(got) != 0 {
		t.Fatalf("the header that stayed was downloaded again: %q", got)
	}
	if got := readPart(t, env, "video", 3*mib, 64*kib); !bytes.Equal(got, content[3*mib:3*mib+64*kib]) {
		t.Fatal("the evicted middle reads different")
	}
	if len(mediaRanges(env)) == 0 {
		t.Fatal("the evicted middle was read with no download")
	}
}
