package core_test

import (
	"bytes"
	"context"
	"fmt"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestReadAnyOffsetWithRangeRequests(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	content := testenv.RandomBytes(3<<20+17, 1)
	env.Emu.WriteFile(env.Emu.Root(), "f.bin", content)
	env.Start()
	a := env.MustResolve("f.bin")
	fh, err := env.FS.Open(context.Background(), a.Ino, syscall.O_RDONLY)
	if err != nil {
		t.Fatal(err)
	}
	defer env.FS.Release(fh)

	env.Emu.ResetRequests()
	requireReadsMatch(t, env, fh, content)
	n, err := env.FS.Read(context.Background(), fh, int64(len(content)), make([]byte, 10))
	if n != 0 || err != nil {
		t.Fatalf("read at EOF = %d, %v", n, err)
	}

	asked := askedBytes(t, env)
	if asked == 0 || asked >= int64(len(content))/4 {
		t.Fatalf("three reads of 4 KiB downloaded %d bytes", asked)
	}
}

func TestOpenRefusesFolders(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Emu.Mkdir(env.Emu.Root(), "d")
	env.Start()
	_, err := env.FS.Open(context.Background(), env.MustResolve("d").Ino, syscall.O_RDONLY)
	testenv.RequireErrno(t, err, syscall.EISDIR)
}

func TestReadWithDriveDownIsEIO(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Emu.WriteFile(env.Emu.Root(), "f", []byte("hello"))
	env.Start()
	fh, err := env.FS.Open(context.Background(), env.MustResolve("f").Ino, syscall.O_RDONLY)
	if err != nil {
		t.Fatal(err)
	}
	env.Emu.SetOffline(true)
	_, err = env.FS.Read(context.Background(), fh, 0, make([]byte, 5))
	testenv.RequireErrno(t, err, syscall.EIO)
}

func TestCutDownloadIsAnErrorNotAShortRead(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Emu.WriteFile(env.Emu.Root(), "f", testenv.RandomBytes(1<<20, 2))
	env.Start()
	fh, err := env.FS.Open(context.Background(), env.MustResolve("f").Ino, syscall.O_RDONLY)
	if err != nil {
		t.Fatal(err)
	}
	env.Emu.CutMedia(-1, 1000)
	n, err := env.FS.Read(context.Background(), fh, 0, make([]byte, 64<<10))
	if n != 0 {
		t.Fatalf("read over a cut connection gave %d bytes", n)
	}
	testenv.RequireErrno(t, err, syscall.EIO)
}

func TestReadOfADownloadGoneSilentIsEIO(t *testing.T) {
	env := testenv.New(t)
	content := testenv.RandomBytes(256<<10, 130)
	env.Emu.WriteFile(env.Emu.Root(), "f", content)
	env.Start()
	fh, err := env.FS.Open(context.Background(), env.MustResolve("f").Ino, syscall.O_RDONLY)
	if err != nil {
		t.Fatal(err)
	}
	defer env.FS.Release(fh)
	env.Emu.StallMedia(10 << 10)
	start := time.Now()
	_, err = env.FS.Read(context.Background(), fh, 0, make([]byte, 128<<10))
	testenv.RequireErrno(t, err, syscall.EIO)

	if took := time.Since(start); took < 900*time.Millisecond || took > 5*time.Second {
		t.Fatalf("the read failed after %v, want about a second", took)
	}

	env.Emu.SetOffline(true)
	start = time.Now()
	_, err = env.FS.Read(context.Background(), fh, 0, make([]byte, 128<<10))
	testenv.RequireErrno(t, err, syscall.EIO)
	if took := time.Since(start); took > 150*time.Millisecond {
		t.Fatalf("the read after the silence failed after %v, want at once", took)
	}

	env.Emu.SetOffline(false)
	env.Emu.StallMedia(-1)
	if got := mustRead(t, env, fh, 0, 256<<10); !bytes.Equal(got, content) {
		t.Fatal("after the silence the file reads different")
	}
}

func requireReadsMatch(t *testing.T, env *testenv.Env, fh uint64, content []byte) {
	t.Helper()
	for _, off := range []int64{0, 1<<20 + 3, int64(len(content)) - 100} {
		buf := make([]byte, 4096)
		n, err := env.FS.Read(context.Background(), fh, off, buf)
		if err != nil {
			t.Fatal(err)
		}
		want := content[off:min(off+4096, int64(len(content)))]
		if !bytes.Equal(buf[:n], want) {
			t.Fatalf("read at %d: %d bytes, not the file's", off, n)
		}
	}
}

func askedBytes(t *testing.T, env *testenv.Env) (asked int64) {
	t.Helper()
	for _, r := range mediaRanges(env) {
		var from, to int64
		if _, err := fmt.Sscanf(r, "bytes=%d-%d", &from, &to); err != nil {
			t.Fatalf("download without a range: %q", r)
		}
		asked += to - from + 1
	}
	return asked
}
