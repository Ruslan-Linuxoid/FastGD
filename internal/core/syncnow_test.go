package core_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func requireSyncNowSendsAll(t *testing.T, env *testenv.Env) {
	t.Helper()
	left, err := env.FS.SyncNow(context.Background())
	if err != nil || left != 0 {
		t.Fatalf("the sync left %d operations: %v", left, err)
	}
	for name, want := range map[string]string{"failed.txt": "failed once", "open.txt": "written so far", "empty.txt": ""} {
		if f := inDrive(t, env, name); !bytes.Equal(f.Content, []byte(want)) {
			t.Fatalf("Drive holds %q as %q, want %q", name, f.Content, want)
		}
	}
}

func TestSyncNowSendsWhatWaitsOutADelay(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	ctx := context.Background()
	root := env.MustResolve("").Ino

	writeFile(t, env, "", "failed.txt", []byte("failed once"))
	env.Emu.SetOffline(true)
	env.FS.SyncOnce(ctx)
	env.Emu.SetOffline(false)
	if it := item(t, env, "failed.txt"); !it.Dirty || it.Attempts != 1 {
		t.Fatalf("scenario not built: the file is dirty %v after %d tries", it.Dirty, it.Attempts)
	}

	_, open, err := env.FS.Create(root, "open.txt", syscall.O_RDWR)
	if err != nil {
		t.Fatal(err)
	}
	defer env.FS.Release(open)
	write(t, env, open, 0, []byte("written so far"))
	_, empty, err := env.FS.Create(root, "empty.txt", syscall.O_RDWR)
	if err != nil {
		t.Fatal(err)
	}
	defer env.FS.Release(empty)
	if n := env.FS.SyncOnce(ctx); n != 0 {
		t.Fatalf("scenario not built: an upload round sent %d operations", n)
	}
	if n := len(env.Emu.All()); n != 0 {
		t.Fatalf("scenario not built: Drive holds %d items", n)
	}

	requireSyncNowSendsAll(t, env)

	write(t, env, open, 14, []byte(", and more"))
	env.FS.Release(open)
	uploadAll(t, env)
	if f := inDrive(t, env, "open.txt"); string(f.Content) != "written so far, and more" {
		t.Fatalf("after the sync the open file went to Drive as %q", f.Content)
	}
}

func TestSyncNowTellsWhatDriveDidNotTake(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	ctx := context.Background()
	mkdir(t, env, "", "dir")
	writeFile(t, env, "dir", "in.txt", []byte("in a new folder"))
	writeFile(t, env, "", "stuck.txt", []byte("stuck"))

	env.Emu.SetOffline(true)
	start := time.Now()
	left, err := env.FS.SyncNow(ctx)
	if err != nil || left != 3 {
		t.Fatalf("without the network the sync left %d operations: %v, want all three", left, err)
	}
	if took := time.Since(start); took > time.Minute {
		t.Fatalf("without the network the sync took %v", took)
	}
	env.Emu.SetOffline(false)

	env.Emu.SetUploadFault(func(name string) int {
		if name == "stuck.txt" {
			return http.StatusBadRequest
		}
		return 0
	})

	left, err = env.FS.SyncNow(ctx)
	if err != nil || left != 1 {
		t.Fatalf("with one file refused the sync left %d operations: %v", left, err)
	}
	if f := inDrive(t, env, "dir", "in.txt"); string(f.Content) != "in a new folder" {
		t.Fatalf("the file in the new folder went to Drive as %q", f.Content)
	}
	if it := item(t, env, "stuck.txt"); !it.Dirty || it.LastError == "" {
		t.Fatalf("the refused file is dirty %v with the error %q", it.Dirty, it.LastError)
	}
}

func TestSyncNowKeepsTheWaitsDriveSet(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	ctx := context.Background()
	writeFile(t, env, "", "refused.txt", []byte("refused"))
	env.Emu.SetUploadFault(func(string) int { return http.StatusBadRequest })
	env.FS.SyncOnce(ctx)
	tries := item(t, env, "refused.txt").Attempts
	if tries == 0 {
		t.Fatal("scenario not built: the file was not tried")
	}
	for range 3 {
		if left, err := env.FS.SyncNow(ctx); err != nil || left != 1 {
			t.Fatalf("the sync left %d operations: %v", left, err)
		}
	}
	if it := item(t, env, "refused.txt"); it.Attempts != tries {
		t.Fatalf("three syncs tried the refused file %d more times", it.Attempts-tries)
	}
}

func TestSyncCalledOffWhileTheUploadsAreTakenEndsAtOnce(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	for i := range 4 {
		writeFile(t, env, "", fmt.Sprintf("long%d.bin", i), []byte("long upload"))
	}
	env.Clock.Advance(time.Minute)
	release := env.Emu.HoldUploads()
	held := make(chan struct{})
	go func() {
		env.FS.SyncOnce(context.Background())
		close(held)
	}()
	waitUntil(t, func() bool { return env.Emu.UploadsWaiting() == 4 })
	writeFile(t, env, "", "late.txt", []byte("late"))
	env.Clock.Advance(time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	synced := make(chan error, 1)
	go func() {
		_, err := env.FS.SyncNow(ctx)
		synced <- err
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case err := <-synced:
		if err == nil {
			t.Fatal("a sync called off reports it sent everything")
		}
	case <-time.After(5 * time.Second):

		release()
		t.Fatal("a sync called off while the uploads are taken does not end")
	}
	release()
	<-held
	uploadAll(t, env)
	if f := inDrive(t, env, "late.txt"); string(f.Content) != "late" {
		t.Fatal("the file of the sync called off did not go in a later round")
	}
}
