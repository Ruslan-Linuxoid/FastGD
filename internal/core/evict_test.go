package core_test

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func du(t *testing.T, env *testenv.Env) int64 {
	t.Helper()
	n, err := duOf(env)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func duOf(env *testenv.Env) (int64, error) {
	var n int64
	err := filepath.WalkDir(core.ContentDir(env.Dir), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		var st syscall.Stat_t
		if err := syscall.Stat(p, &st); err != nil {
			return err
		}
		n += st.Blocks * 512
		return nil
	})
	return n, err
}

func files(env *testenv.Env, count, size int) [][]byte {
	out := make([][]byte, count)
	for i := range out {
		out[i] = testenv.RandomBytes(size, uint64(200+i))
		env.Emu.WriteFile(env.Emu.Root(), "f"+string(rune('0'+i)), out[i])
	}
	return out
}

func cached(t *testing.T, env *testenv.Env, path string) int64 {
	t.Helper()
	return cachedBytes(pieces(t, env, path))
}

func TestStartNeedsAFifthOfTheSpaceAtLeastTheLeastLimit(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.CacheSize = 2 << 30
	env.Tune = func(o *core.Options) {
		o.MinCacheLimit = 1 << 30
		o.CacheFree = func() (int64, error) { return 4 << 30, nil }
	}
	_, err := env.Open()
	if err == nil || !strings.Contains(err.Error(), "less than the least cache") {
		t.Fatalf("a start with a fifth of 4 GiB, less than 1 GiB, got %v", err)
	}
}

func TestDirtyFilesAreNeverEvicted(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)

	env.CacheSize = 1536 * kib
	content := files(env, 4, 400*kib)
	env.Start()

	readAll(t, env, "f0")
	fh := openWriter(t, env, "f0", 0)
	write(t, env, fh, 10, []byte("edit"))
	env.FS.Release(fh)
	edited := append([]byte(nil), content[0]...)
	copy(edited[10:], "edit")
	created := testenv.RandomBytes(400*kib, 210)
	writeFile(t, env, "", "new.bin", created)
	for i := 1; i < 4; i++ {
		readAll(t, env, "f"+string(rune('0'+i)))
	}
	if got := readAll(t, env, "f0"); !bytes.Equal(got, edited) {
		t.Fatal("the edited file lost bytes to eviction")
	}
	if got := readAll(t, env, "new.bin"); !bytes.Equal(got, created) {
		t.Fatal("the new file lost bytes to eviction")
	}
}

func TestDirtyFilesPastTheLimitWarnAndWritesGoOn(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.CacheSize = mib
	env.Start()
	writeFile(t, env, "", "big.bin", testenv.RandomBytes(2*mib, 211))
	env.FS.WarnDirtyOver()
	env.FS.WarnDirtyOver()
	warned := func() int { return len(env.Logs("dirty files take more than the cache limit")) }
	if warned() != 1 {
		t.Fatalf("crossing the limit warns %d times", warned())
	}
	env.Clock.Advance(10 * time.Minute)
	env.FS.WarnDirtyOver()
	if warned() != 2 {
		t.Fatalf("ten minutes past the limit warn %d times in all", warned())
	}
}

func TestRoomFollowsAChangeInDriveAfterARestart(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	files(env, 1, 512*kib)
	env.Start()
	readClosed(t, env, "f0")
	env.Restart()

	if used := env.FS.CacheUsed(); used != 512*kib+4*kib {
		t.Fatalf("after a restart the cache counts %d bytes", used)
	}
	id := item(t, env, "f0").ID
	env.Emu.Update(id, func(f *emulator.File) { f.Content = testenv.RandomBytes(512*kib, 230) })
	poll(t, env)
	if used := env.FS.CacheUsed(); used != 0 {
		t.Fatalf("the cached version changed in Drive and the cache still counts %d bytes", used)
	}
}

func TestAnEditedFileUploadedAfterARestartIsCountedWhole(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	files(env, 1, 512*kib)
	env.Start()
	readClosed(t, env, "f0")
	fh := openWriter(t, env, "f0", 0)
	write(t, env, fh, 100, []byte("edit"))
	env.FS.Release(fh)
	env.Restart()
	uploadAll(t, env)
	if used := env.FS.CacheUsed(); used != 512*kib+4*kib {
		t.Fatalf("the uploaded file is counted as %d bytes", used)
	}
}

const evictionTime = 11 * time.Second

func name(i int) string { return "f" + string(rune('0'+i)) }

func sparse(t *testing.T, env *testenv.Env, path string) bool {
	t.Helper()
	id := item(t, env, path).ID
	_, err := os.Stat(contentFile(env, id))
	return err == nil
}

func tiny(env *testenv.Env, free *atomic.Int64) {
	env.Tune = func(o *core.Options) {
		o.MinCacheFree = 1
		o.CacheFree = func() (int64, error) { return free.Load(), nil }
	}
}

func TestDownloadsGoPastTheLimitAndEvictionComesBackToIt(t *testing.T) {
	testenv.InBubble(t, func(t *testing.T, env *testenv.Env) {
		env.CacheSize = 2 * mib

		const file = 128*kib + 4*kib
		content := files(env, 30, 128*kib)
		env.Start()
		start := time.Now()
		for i := range 30 {
			if got := readClosed(t, env, name(i)); !bytes.Equal(got, content[i]) {
				t.Fatalf("f%d reads different", i)
			}
		}
		if took := time.Since(start); took > time.Second {
			t.Fatalf("thirty reads into a cache for fifteen took %v: a download waited for room", took)
		}

		if n := du(t, env); n <= 2*mib {
			t.Fatalf("right after the reads the cache takes %d bytes, within its limit: the limit is not soft", n)
		}
		time.Sleep(5 * time.Second)
		if n := du(t, env); n <= 2*mib {
			t.Fatalf("an eviction ran %d s after the one before", 5)
		}
		time.Sleep(evictionTime)

		if n := du(t, env); n > 2*mib || n <= 2*mib-file {
			t.Fatalf("after the eviction the cache takes %d bytes, want just within 2 MiB", n)
		}
	})
}

func readPart(t *testing.T, env *testenv.Env, path string, off int64, n int) []byte {
	t.Helper()
	fh := openFile(t, env, path)
	defer env.FS.Release(fh)
	got := mustRead(t, env, fh, off, n)
	waitUntil(t, func() bool { return holds(t, env, path, off, int64(n)) })
	return got
}

func holds(t *testing.T, env *testenv.Env, path string, off, n int64) bool {
	t.Helper()
	for pos := off; pos < off+n; {
		next := pos
		for _, p := range pieces(t, env, path) {
			if p.Off <= pos && pos < p.Off+p.Len {
				next = p.Off + p.Len
			}
		}
		if next == pos {
			return false
		}
		pos = next
	}
	return true
}
