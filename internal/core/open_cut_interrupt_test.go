package core_test

import (
	"context"
	"errors"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestAnOpenToCutDuringAPollIsInterrupted(t *testing.T) {
	t.Parallel()
	env, _ := fromDrive(t, mixedSize, 1941)
	withoutRevision(t, env)
	id := item(t, env, "f.bin").ID
	swapAtCut(env, id, 1942)
	release := env.FS.HoldPoll()
	var once sync.Once
	defer once.Do(release)

	_, _ = tryRead(env, openFile(t, env, "f.bin"), 0, mixedSize)
	waitUntil(t, func() bool { return metaGets(env, id) > 0 })
	before := metaGets(env, id)
	ino := env.MustResolve("f.bin").Ino
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		fh, err := env.FS.Open(ctx, ino, syscall.O_WRONLY|syscall.O_TRUNC)
		if err == nil {
			env.FS.Release(fh)
		}
		done <- err
	}()

	for deadline := time.Now().Add(10 * time.Second); metaGets(env, id) == before; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("scenario not built: the open did not read the file anew")
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, syscall.EINTR) {
			t.Fatalf("the interrupted open got %v, want EINTR", err)
		}
	case <-time.After(2 * time.Second):
		once.Do(release)
		t.Logf("once the poll was over the open got %v", <-done)
		t.Fatal("the interrupted open waited for the poll")
	}
	if it := item(t, env, "f.bin"); it.Size != mixedSize || it.Dirty {
		t.Fatalf("after the interrupted open the file is %d bytes, changed here %v; want %d, not changed", it.Size,
			it.Dirty, mixedSize)
	}
	once.Do(release)
	requireCutByAnOpen(t, env, ino)
}

func requireCutByAnOpen(t *testing.T, env *testenv.Env, ino uint64) {
	t.Helper()
	fh, err := env.FS.Open(context.Background(), ino, syscall.O_WRONLY|syscall.O_TRUNC)
	if err != nil {
		t.Fatal(err)
	}
	env.FS.Release(fh)
	if size := env.MustResolve("f.bin").Size; size != 0 {
		t.Fatalf("after the open that cut it the file is %d bytes", size)
	}
}
