package core_test

import (
	"bytes"
	"context"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestWriteThatLeavesTheDiskTooFullGetsNoSpace(t *testing.T) {
	testenv.InBubble(t, func(t *testing.T, env *testenv.Env) {
		const least = 500 * mib
		var free atomic.Int64
		free.Store(4 << 30)
		env.Tune = func(o *core.Options) {
			o.MinCacheFree = least
			o.CacheFree = func() (int64, error) { return free.Load(), nil }
		}
		content := files(env, 1, 128*kib)
		env.Start()
		writeFile(t, env, "", "mine.txt", []byte("before"))
		if got := readClosed(t, env, name(0)); !bytes.Equal(got, content[0]) {
			t.Fatal("scenario not built: the cached file reads different")
		}

		free.Store(least - 200*kib)
		time.Sleep(time.Second)

		fh := openWriter(t, env, "mine.txt", 0)
		defer env.FS.Release(fh)
		if _, err := env.FS.Write(context.Background(), fh, 0, make([]byte, 64*kib)); err != syscall.ENOSPC {
			t.Fatalf("a write that leaves the disk too full got %v, want ENOSPC", err)
		}
		if a := env.MustResolve("mine.txt"); a.Size != 6 {
			t.Fatalf("the refused write left the file with %d bytes", a.Size)
		}
		if got := readAll(t, env, "mine.txt"); string(got) != "before" {
			t.Fatalf("the refused write left %q in the file", got)
		}
		_, lock := createFile(t, env, "", ".~lock.mine.txt#")
		defer env.FS.Release(lock)
		if _, err := env.FS.Write(context.Background(), lock, 0, make([]byte, 64*kib)); err != syscall.ENOSPC {
			t.Fatalf("a write into a service file got %v, want ENOSPC", err)
		}
		if len(env.Logs("no room")) == 0 {
			t.Fatal("the log does not tell of the write that got no room")
		}

		if got := readClosed(t, env, name(0)); !bytes.Equal(got, content[0]) {
			t.Fatal("the cached file does not read with the disk nearly full")
		}
		rename(t, env, "", "mine.txt", "", "renamed.txt")

		free.Store(least - 100*kib)
		time.Sleep(time.Second)
		if _, err := env.FS.Write(context.Background(), fh, 0, make([]byte, 8*kib)); err != nil {
			t.Fatalf("a write that fits with what may be evicted got %v", err)
		}

		free.Store(4 << 30)
		time.Sleep(time.Second)
		if _, err := env.FS.Write(context.Background(), lock, 0, make([]byte, 64*kib)); err != nil {
			t.Fatalf("a write with room on the disk got %v", err)
		}
	})
}

func TestDirtyFilesPastTheLimitCountByTheirDisk(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.CacheSize = mib
	env.Emu.WriteFile(env.Emu.Root(), "f.bin", testenv.RandomBytes(4*mib, 212))
	env.Start()

	changedUnfetched(t, env)
	fh := openWriter(t, env, "f.bin", 0)
	write(t, env, fh, 8192, testenv.RandomBytes(4096, 213))
	env.FS.Release(fh)
	if it := item(t, env, "f.bin"); !it.Dirty || it.Size != 4*mib || du(t, env) > 64*kib {
		t.Fatalf("scenario not built: the file is dirty %v, of %d bytes, the cache takes %d on disk", it.Dirty, it.Size, du(t,
			env))
	}
	env.FS.WarnDirtyOver()
	if lines := env.Logs("dirty files take more than the cache limit"); len(lines) != 0 {
		t.Fatalf("a page written into a large file warns of the limit: %q", lines)
	}
}

func TestReadsFromAGoingDownloadCountForThePiece(t *testing.T) {
	testenv.InBubble(t, func(t *testing.T, env *testenv.Env) {
		env.CacheSize = mib
		content := files(env, 3, 400*kib)
		env.Start()
		env.Emu.SetMediaDelay(2 * time.Second)
		var readers sync.WaitGroup
		for range 3 {
			readers.Go(func() {
				fh := openFile(t, env, "f0")

				defer env.FS.Release(fh)
				if got := mustRead(t, env, fh, 0, 400*kib); !bytes.Equal(got, content[0]) {
					t.Error("f0 reads different from a download that is going")
				}
			})
		}
		readers.Wait()
		waitUntil(t, func() bool { return cached(t, env, "f0") == 400*kib })
		env.Emu.SetMediaDelay(0)
		for range 2 {
			readClosed(t, env, "f1")
		}
		for range 4 {
			readClosed(t, env, "f2")
		}
		time.Sleep(evictionTime)
		if cached(t, env, "f0") != 400*kib || cached(t, env, "f1") != 0 {
			t.Fatalf("f0, read by three at once as it came, has %d bytes cached, f1, read twice, %d", cached(t, env, "f0"),
				cached(t, env, "f1"))
		}
	})
}
