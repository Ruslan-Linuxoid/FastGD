package core_test

import (
	"bytes"
	"context"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestOpenFilesAreNeitherEvictedNorCounted(t *testing.T) {
	testenv.InBubble(t, func(t *testing.T, env *testenv.Env) {
		env.CacheSize = mib
		files(env, 4, 400*kib)
		env.Start()
		open := openFile(t, env, "f0")
		mustRead(t, env, open, 0, 400*kib)
		readClosed(t, env, "f1")
		readClosed(t, env, "f2")
		time.Sleep(evictionTime)

		for i := range 3 {
			if cached(t, env, name(i)) != 400*kib {
				t.Fatalf("f%d was evicted with the closed files within the limit", i)
			}
		}
		readClosed(t, env, "f3")
		time.Sleep(evictionTime)
		if cached(t, env, "f0") != 400*kib || cached(t, env, "f1") != 0 {
			t.Fatalf("the open f0 has %d bytes cached, the closed f1 %d", cached(t, env, "f0"), cached(t, env, "f1"))
		}
	})
}

func TestAnOpenFileLargerThanTheCacheReadsThroughAndStays(t *testing.T) {
	testenv.InBubble(t, func(t *testing.T, env *testenv.Env) {
		env.CacheSize = mib
		content := files(env, 1, 4*mib)
		env.Start()
		fh, err := env.FS.Open(context.Background(), env.MustResolve("f0").Ino, syscall.O_RDONLY)
		if err != nil {
			t.Fatal(err)
		}
		var got []byte
		for off := int64(0); off < 4*mib; off += 64 * kib {
			got = append(got, mustRead(t, env, fh, off, 64*kib)...)
			time.Sleep(time.Second)
		}
		if !bytes.Equal(got, content[0]) {
			t.Fatal("the file read through the cache differs")
		}
		time.Sleep(evictionTime)
		if cached(t, env, "f0") != 4*mib {
			t.Fatalf("the open file has %d bytes cached of the 4 MiB read", cached(t, env, "f0"))
		}
		env.FS.Release(fh)
		time.Sleep(evictionTime)

		if n := cached(t, env, "f0"); n == 0 || du(t, env) > mib {
			t.Fatalf("closed, the file larger than the cache has %d bytes cached and the cache takes %d", n, du(t, env))
		}
	})
}

func TestADownloadDoesNotWaitForTheRoomDirtyFilesTake(t *testing.T) {
	testenv.InBubble(t, func(t *testing.T, env *testenv.Env) {
		env.CacheSize = mib
		content := files(env, 1, 300*kib)
		env.Start()
		writeFile(t, env, "", "dirty.bin", testenv.RandomBytes(900*kib, 212))
		start := time.Now()
		if got := readClosed(t, env, "f0"); !bytes.Equal(got, content[0]) {
			t.Fatal("the file reads different")
		}
		if took := time.Since(start); took > time.Second {
			t.Fatalf("the read took %v with a dirty file in the cache", took)
		}

		time.Sleep(evictionTime)
		if cached(t, env, "f0") != 300*kib {
			t.Fatal("the clean file was evicted for the room a dirty file takes")
		}
	})
}

func TestTheLimitFollowsTheFreeSpace(t *testing.T) {
	testenv.InBubble(t, func(t *testing.T, env *testenv.Env) {
		env.CacheSize = 8 * mib
		var free atomic.Int64
		free.Store(100 * mib)
		tiny(env, &free)
		files(env, 6, mib)
		env.Start()
		for i := range 6 {
			readClosed(t, env, name(i))
		}
		time.Sleep(evictionTime)
		if du(t, env) < 6*mib {
			t.Fatalf("the cache holds %d bytes of 6 MiB read", du(t, env))
		}

		free.Store(18 * mib)
		env.Clock.Advance(2 * time.Second)
		time.Sleep(evictionTime)
		if l := env.FS.CacheLimit(); l >= 6*mib || du(t, env) > l {
			t.Fatalf("with 18 MiB free the limit is %d and the cache takes %d", l, du(t, env))
		}
		if len(env.Logs("the cache limit changed")) < 2 {
			t.Fatal("the log does not tell of the new limit")
		}
	})
}

func TestALimitBelowTheLeastWarns(t *testing.T) {
	testenv.InBubble(t, func(t *testing.T, env *testenv.Env) {
		env.CacheSize = 2 << 30
		var free atomic.Int64
		free.Store(20 << 30)
		env.Tune = func(o *core.Options) {
			o.MinCacheLimit = 1 << 30
			o.CacheFree = func() (int64, error) { return free.Load(), nil }
		}
		env.Start()
		free.Store(3 << 30)
		env.Clock.Advance(2 * time.Second)
		time.Sleep(time.Second)
		if len(env.Logs("fell below the least cache_size")) != 1 {
			t.Fatal("a limit below 1 GiB at work does not warn")
		}
	})
}

func TestADiskNearlyFullStopsDownloads(t *testing.T) {
	testenv.InBubble(t, func(t *testing.T, env *testenv.Env) {
		var free atomic.Int64
		free.Store(10 << 30)
		env.Tune = func(o *core.Options) { o.CacheFree = func() (int64, error) { return free.Load(), nil } }
		content := files(env, 2, 64*kib)
		env.Start()
		readClosed(t, env, "f0")

		free.Store(499 << 20)
		time.Sleep(time.Second)
		env.Emu.ResetRequests()
		fh := openFile(t, env, "f1")
		start := time.Now()
		if _, err := env.FS.Read(context.Background(), fh, 0, make([]byte, 4096)); err != syscall.EIO {
			t.Fatalf("a read of what is not cached, with the disk nearly full, got %v, want EIO", err)
		}
		if took := time.Since(start); took != 0 || len(mediaRanges(env)) != 0 {
			t.Fatalf("the refusal took %v and asked Drive %v", took, mediaRanges(env))
		}
		if len(env.Logs("nearly full: nothing is downloaded")) != 1 {
			t.Fatal("the log does not warn of the disk")
		}

		if got := readAll(t, env, "f0"); !bytes.Equal(got, content[0]) {
			t.Fatal("the cached file reads different")
		}
		_, made := createFile(t, env, "", "new.bin")
		if _, err := env.FS.Write(context.Background(), made, 0,
			[]byte("written with the disk nearly full")); err != syscall.ENOSPC {
			t.Fatalf("a write with the disk nearly full got %v, want ENOSPC", err)
		}
		env.FS.Release(made)
		free.Store(600 << 20)
		time.Sleep(time.Second)

		w := openWriter(t, env, "f1", 0)
		write(t, env, w, 100, []byte("edit"))
		env.FS.Release(w)
		copy(content[1][100:], "edit")
		if got := readAll(t, env, "f1"); !bytes.Equal(got, content[1]) {
			t.Fatal("with room on the disk again the file does not read")
		}
	})
}

func TestWhatACrashLeftIsReclaimedTwoMinutesAfterAStart(t *testing.T) {
	testenv.InBubble(t, func(t *testing.T, env *testenv.Env) {
		files(env, 2, 512*kib)
		env.Start()
		readClosed(t, env, "f0")
		readClosed(t, env, "f1")
		before := du(t, env)

		if err := env.FS.Store().DeletePieces(meta.Ino(env.MustResolve("f0").Ino)); err != nil {
			t.Fatal(err)
		}
		env.Restart()
		time.Sleep(time.Minute)

		readClosed(t, env, "f1")
		if after := du(t, env); after != before {
			t.Fatalf("a minute after the start the cache takes %d bytes, %d before: its directory was gone over early", after,
				before)
		}
		time.Sleep(time.Minute + time.Second)
		if after := du(t, env); after >= before || sparse(t, env, "f0") || cached(t, env, "f1") != 512*kib {
			t.Fatalf("two minutes after the start the cache takes %d bytes, %d before", after, before)
		}
	})
}

func TestDatabaseThatKeptTheOrderOfEvictionOpens(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	content := files(env, 1, 64*kib)
	env.Start()
	readClosed(t, env, "f0")
	if err := env.FS.Close(); err != nil {
		t.Fatal(err)
	}
	env.FS = nil
	db := env.Dir + "/meta.db"
	for _, q := range []string{
		`ALTER TABLE pieces ADD COLUMN hits INTEGER NOT NULL DEFAULT 1`,
		`ALTER TABLE pieces ADD COLUMN value REAL NOT NULL DEFAULT 0`,
		`CREATE INDEX pieces_value ON pieces(value)`,
		`CREATE TABLE evicted (ino INTEGER NOT NULL, off INTEGER NOT NULL, end INTEGER NOT NULL, hits INTEGER NOT NULL)`,
		`INSERT INTO state(key, value) VALUES ('evict_floor', '0.5')`,
		`ALTER TABLE items DROP COLUMN service`,
		`PRAGMA user_version = 15`,
	} {
		if err := execSQL(db, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	env.Start()
	env.Emu.ResetRequests()
	if got := readAll(t, env, "f0"); !bytes.Equal(got, content[0]) || len(mediaRanges(env)) != 0 {
		t.Fatal("after the upgrade the cached file is not read from the cache")
	}
}
