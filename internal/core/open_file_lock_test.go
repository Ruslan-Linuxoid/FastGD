package core_test

import (
	"context"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestAFileLockedForLongHoldsNoOtherFile(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	writeFile(t, env, "", "other.txt", []byte("other"))
	other := env.MustResolve("other.txt").Ino
	slow, fh := createFile(t, env, "", "slow.bin")
	write(t, env, fh, 0, []byte("slow"))
	env.FS.Release(fh)
	release := env.FS.Edits().Lock(item(t, env, "slow.bin").Item).Unlock
	held := true
	defer func() {
		if held {
			release()
		}
	}()

	env.Clock.Advance(20 * time.Minute)
	within(t, "the upload round", func() {
		if n := env.FS.SyncOnce(context.Background()); n != 1 {
			t.Errorf("the round took %d uploads, want that of the other file alone", n)
		}
	})
	inDrive(t, env, "other.txt")
	within(t, "the save of sizes", env.FS.SaveSizes)

	opened := make(chan uint64)
	go func() {
		again, err := env.FS.Open(context.Background(), slow, syscall.O_WRONLY)
		if err != nil {
			t.Errorf("open: %v", err)
		}
		opened <- again
	}()
	time.Sleep(50 * time.Millisecond)
	within(t, "the stat of another file", func() {
		if _, err := env.FS.GetAttr(other); err != nil {
			t.Error(err)
		}
	})
	within(t, "the listing of the folder", func() { env.Names("") })
	select {
	case <-opened:
		t.Fatal("scenario not built: the open of the locked file did not wait")
	default:
	}

	release()
	held = false
	env.FS.Release(<-opened)
	uploadAll(t, env)
	inDrive(t, env, "slow.bin")
}

func TestFsyncHoldsNoLockWhileItFlushes(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	ino, fh := createFile(t, env, "", "f.bin")
	write(t, env, fh, 0, []byte("data"))
	flushing, goOn := make(chan struct{}), make(chan struct{})
	env.FS.SyncLocal(func(string) error {
		close(flushing)
		<-goOn
		return nil
	})
	done := make(chan error)
	go func() { done <- env.FS.Fsync(fh) }()
	within(t, "the flush", func() { <-flushing })
	within(t, "the stat of the file", func() {
		if a, err := env.FS.GetAttr(ino); err != nil || a.Size != 4 {
			t.Errorf("stat during the flush: %+v, %v", a, err)
		}
	})
	write(t, env, fh, 4, []byte("more"))
	close(goOn)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if it := item(t, env, "f.bin"); it.Size != 8 {
		t.Fatalf("after the fsync the database holds the size %d", it.Size)
	}
}
