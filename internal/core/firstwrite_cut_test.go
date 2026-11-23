package core_test

import (
	"bytes"
	"context"
	"fmt"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func requireCutDownloadedByPages(t *testing.T, env *testenv.Env, keep int) {
	t.Helper()

	for _, r := range mediaRanges(env) {
		var from, to int64
		if _, err := fmt.Sscanf(r, "bytes=%d-%d", &from, &to); err != nil || to >= int64((keep+4095)/4096*4096) {
			t.Fatalf("the cut to %d bytes downloaded %q: %v", keep, r, err)
		}
	}
}

func TestCutWaitsForTheBytesItKeeps(t *testing.T) {
	t.Parallel()
	testenv.InBubble(t, func(t *testing.T, env *testenv.Env) {
		content := startWith(env, 2<<20, 860)
		env.Emu.SetMediaDelay(time.Second)
		ino := env.MustResolve("f.bin").Ino
		const keep = 100000
		start := time.Now()
		done := make(chan error, 1)
		go func() { done <- env.FS.Truncate(context.Background(), ino, keep) }()
		synctest.Wait()
		select {
		case err := <-done:
			t.Fatalf("the cut answered before the bytes it keeps came: %v", err)
		default:
		}
		if it := item(t, env, "f.bin"); it.Dirty || it.Size != int64(len(content)) {
			t.Fatalf("before the bytes came the file is changed here %v, of %d bytes", it.Dirty, it.Size)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if took := time.Since(start); took < time.Second {
			t.Fatalf("the cut answered after %v, the download takes a second", took)
		}
		requireCutDownloadedByPages(t, env, keep)
		env.Emu.SetMediaDelay(0)
		env.Emu.ResetRequests()
		env.FS.FillBases(context.Background())
		requireRanges(t, env)
		requireWholeOffline(t, env, content[:keep])
		uploadAll(t, env)
		if f := inDrive(t, env, "f.bin"); !bytes.Equal(f.Content, content[:keep]) {
			t.Fatalf("Drive holds %d bytes, want the first %d of the file", len(f.Content), keep)
		}
	})
	t.Run("grown", func(t *testing.T) {
		t.Parallel()
		env, content := fromDrive(t, 300<<10, 861)
		if err := env.FS.Truncate(context.Background(), env.MustResolve("f.bin").Ino, 400<<10); err != nil {
			t.Fatal(err)
		}
		env.FS.FillBases(context.Background())
		want := append(append([]byte(nil), content...), make([]byte, 100<<10)...)
		requireWholeOffline(t, env, want)
	})
}

func requireFailedCutChangedNothing(t *testing.T, env *testenv.Env, before tried) {
	t.Helper()
	it := item(t, env, "f.bin")
	if it.Dirty || it.Base != 0 || it.Size != before.Size || it.MD5 != before.MD5 {
		t.Fatalf("after the cut that failed the file is changed here %v, base %d, size %d", it.Dirty, it.Base, it.Size)
	}
	if left, err := env.FS.Store().HasOps(); err != nil || left {
		t.Fatalf("after the cut that failed an operation waits for Drive: %v", err)
	}
}

func TestCutFailsWithoutTheBytesItKeeps(t *testing.T) {
	t.Parallel()
	for name, cut := range map[string]func(env *testenv.Env, id string){
		"no network":       func(env *testenv.Env, _ string) { env.Emu.SetOffline(true) },
		"deleted for good": func(env *testenv.Env, id string) { env.Emu.Delete(id) },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			env, content := fromDrive(t, 256<<10, 862)
			before := item(t, env, "f.bin")
			cut(env, before.ID)
			if err := env.FS.Truncate(context.Background(), uint64(before.Ino), 100000); err != syscall.EIO {
				t.Fatalf("the cut got %v, want EIO", err)
			}
			requireFailedCutChangedNothing(t, env, before)
			if name == "no network" {
				env.Emu.SetOffline(false)
				if got := readAll(t, env, "f.bin"); !bytes.Equal(got, content) {
					t.Fatal("the file does not read as its Drive version")
				}
			}
		})
	}
}
