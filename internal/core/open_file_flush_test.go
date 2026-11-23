package core_test

import (
	"bytes"
	"context"
	"slices"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func requireFailedFlushKeptFileBack(t *testing.T, env *testenv.Env) {
	t.Helper()
	if it := item(t, env, "f.bin"); !it.Dirty || it.LastError == "" {
		t.Fatalf("after a flush that failed the file is dirty %v with the error %q", it.Dirty, it.LastError)
	}
	if ps := pieces(t, env, "f.bin"); len(ps) != 0 {
		t.Fatalf("a file that is not on the disk is recorded as cached: %v", ps)
	}
	for _, f := range env.Emu.All() {
		if f.Name == "f.bin" {
			t.Fatal("a file that did not reach the disk went to Drive")
		}
	}
}

func TestUploadedFileIsOnDiskBeforeItIsClean(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	content := testenv.RandomBytes(300<<10, 68)
	writeFile(t, env, "", "f.bin", content)
	var syncs []bool
	failing := true
	env.FS.SyncLocal(func(id string) error {
		it := item(t, env, "f.bin")
		if id != it.ID {
			t.Errorf("the flush is of %s, want %s", id, it.ID)
		}
		syncs = append(syncs, it.Dirty)
		if failing {
			return syscall.EIO
		}
		return nil
	})
	env.Clock.Advance(time.Minute)
	env.FS.SyncOnce(context.Background())
	requireFailedFlushKeptFileBack(t, env)
	failing = false
	uploadAll(t, env)
	if it := item(t, env, "f.bin"); it.Dirty {
		t.Fatal("the file is still dirty")
	}
	if !slices.Equal(syncs, []bool{true, true}) {
		t.Fatalf("the flushes found the file dirty %v, want one before each try to mark it clean", syncs)
	}
	if f := inDrive(t, env, "f.bin"); !bytes.Equal(f.Content, content) {
		t.Fatal("Drive has other content")
	}
}

func TestCuttingAnEmptyFileSendsNoContent(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id := env.Emu.WriteFile(env.Emu.Root(), "empty.txt", nil)
	env.Start()
	before := item(t, env, "empty.txt").Mtime
	env.Clock.Advance(time.Hour)
	env.Emu.ResetRequests()
	fh := openWriter(t, env, "empty.txt", syscall.O_TRUNC)
	env.FS.Release(fh)
	it := item(t, env, "empty.txt")
	if it.Dirty || !it.Mtime.After(before) {
		t.Fatalf("the empty file is dirty %v, its time moved from %v to %v", it.Dirty, before, it.Mtime)
	}
	uploadAll(t, env)
	if content, _ := uploads(env); content != 0 {
		t.Fatalf("%d requests sent content for an empty file", content)
	}
	if f, _ := env.Emu.Get(id); len(f.Content) != 0 {
		t.Fatalf("Drive holds %d bytes of the empty file", len(f.Content))
	}
}

func TestFsyncAfterTheUploadFlushesNothing(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	var flushes atomic.Int64
	env.FS.SyncLocal(func(string) error {
		flushes.Add(1)
		return nil
	})
	writeFile(t, env, "", "f.txt", []byte("written"))
	uploadAll(t, env)
	fh := openWriter(t, env, "f.txt", 0)
	defer env.FS.Release(fh)
	if it := item(t, env, "f.txt"); it.Dirty {
		t.Fatal("scenario not built: the file is not in Drive")
	}
	before := flushes.Load()
	if before == 0 {
		t.Fatal("the upload marked the file clean without a flush of its data")
	}
	if err := env.FS.Fsync(fh); err != nil {
		t.Fatal(err)
	}
	if after := flushes.Load(); after != before {
		t.Fatalf("the fsync of a clean file flushed %d more times", after-before)
	}
}
