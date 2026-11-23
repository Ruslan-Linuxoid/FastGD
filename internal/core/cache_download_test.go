package core_test

import (
	"bytes"
	"context"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestCachedPiecesSurviveRestartWithDriveDown(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	content := testenv.RandomBytes(big, 8)
	env.Emu.WriteFile(env.Emu.Root(), "cached.bin", content)
	env.Emu.WriteFile(env.Emu.Root(), "cold.bin", []byte("never read"))
	env.Start()
	fh := openFile(t, env, "cached.bin")
	mustRead(t, env, fh, 0, len(content))
	settled(t, env, 1)

	env.Restart()
	env.Emu.SetOffline(true)
	fh = openFile(t, env, "cached.bin")
	if got := mustRead(t, env, fh, 0, len(content)); !bytes.Equal(got, content) {
		t.Fatal("cached content differs after restart")
	}
	cold := openFile(t, env, "cold.bin")
	_, err := env.FS.Read(context.Background(), cold, 0, make([]byte, 10))
	testenv.RequireErrno(t, err, syscall.EIO)
}

func TestCutDownloadResumesWhereItBroke(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)

	content := testenv.RandomBytes(mib, 9)
	env.Emu.WriteFile(env.Emu.Root(), "f.bin", content)
	env.Start()
	fh := openFile(t, env, "f.bin")

	env.Emu.ResetRequests()
	env.Emu.CutMedia(1, 100*kib)
	if got := mustRead(t, env, fh, 0, mib); !bytes.Equal(got, content[:mib]) {
		t.Fatal("bytes differ from Drive's")
	}
	if got := mediaRanges(env); !slices.Contains(got, rangeHeader(100*kib, mib)) {
		t.Fatalf("downloads %q, want the rest from %d after the cut", got, 100*kib)
	}
	settled(t, env, 2)
	env.Emu.SetOffline(true)
	again := openFile(t, env, "f.bin")
	if got := mustRead(t, env, again, 0, mib); !bytes.Equal(got, content[:mib]) {
		t.Fatal("the cached piece differs from Drive's")
	}
}

func TestBrokenDownloadIsNeverCached(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Emu.WriteFile(env.Emu.Root(), "f.bin", testenv.RandomBytes(mib, 10))
	env.Start()
	fh := openFile(t, env, "f.bin")
	env.Emu.CutMedia(-1, 1000)
	_, err := env.FS.Read(context.Background(), fh, 0, make([]byte, 64*kib))
	testenv.RequireErrno(t, err, syscall.EIO)
	if ps := pieces(t, env, "f.bin"); len(ps) != 0 {
		t.Fatalf("broken downloads were recorded: %v", ps)
	}
	env.Emu.CutMedia(0, 0)
	env.Emu.SetOffline(true)
	env.Restart()
	fh = openFile(t, env, "f.bin")
	_, err = env.FS.Read(context.Background(), fh, 0, make([]byte, 64*kib))
	testenv.RequireErrno(t, err, syscall.EIO)
}

func TestFailedPrefetchGoesAgain(t *testing.T) {
	testenv.InBubble(t, func(t *testing.T, env *testenv.Env) {
		content := testenv.RandomBytes(big, 95)
		env.Emu.WriteFile(env.Emu.Root(), "f.bin", content)
		env.Start()
		fh := openFile(t, env, "f.bin")
		defer env.FS.Release(fh)

		mustRead(t, env, fh, 0, 16*kib)
		mustRead(t, env, fh, 16*kib, 16*kib)
		waitUntil(t, func() bool { return holds(t, env, "f.bin", 0, 48*kib) })

		env.Emu.FailRequests(1<<20, 503, "backendError", 0)
		mustRead(t, env, fh, 32*kib, 16*kib)
		time.Sleep(5 * time.Second)
		if holds(t, env, "f.bin", 48*kib, 64*kib) {
			t.Fatal("scenario not built: the prefetch came while Drive failed")
		}
		env.Emu.FailRequests(0, 0, "", 0)
		time.Sleep(10 * time.Second)
		if !holds(t, env, "f.bin", 48*kib, 64*kib) {
			t.Fatalf("the prefetch Drive failed did not go again: %v", pieces(t, env, "f.bin"))
		}
	})
}
