package core_test

import (
	"context"
	"fmt"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestDailyUploadLimitFromDrive(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	writeFile(t, env, "", "a.bin", []byte("first"))
	env.Emu.SetUploadLimitReached(true)
	env.Clock.Advance(20 * time.Minute)
	env.FS.SyncOnce(context.Background())

	it := item(t, env, "a.bin")
	if !it.Dirty || it.NextTry.Sub(env.Clock.Now()) < 30*time.Second {
		t.Fatalf("after the upload limit the file is dirty=%v, next try in %v", it.Dirty, it.NextTry.Sub(env.Clock.Now()))
	}
	if len(env.Logs("daily upload limit")) == 0 {
		t.Fatal("the upload limit is not in the log")
	}
	requireReadOnly(t, env, 1)

	env.Emu.SetUploadLimitReached(false)
	env.Clock.Advance(2 * time.Hour)
	env.FS.SyncOnce(context.Background())
	if f := inDrive(t, env, "a.bin"); string(f.Content) != "first" {
		t.Fatalf("after the limit lifted Drive has %q", f.Content)
	}
	writeFile(t, env, "", "c.bin", []byte("more"))
}

func TestFullDriveFromDrive(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	writeFile(t, env, "", "a.bin", []byte("first"))
	env.Emu.SetStorageFull(true)
	env.Clock.Advance(20 * time.Minute)
	env.FS.SyncOnce(context.Background())
	if len(env.Logs("storage is full")) == 0 {
		t.Fatal("the full storage is not in the log")
	}
	requireReadOnly(t, env, 1)
	env.Emu.SetStorageFull(false)
	env.Clock.Advance(2 * time.Hour)
	env.FS.SyncOnce(context.Background())
	writeFile(t, env, "", "c.bin", []byte("more"))
}

func requireReadOnly(t *testing.T, env *testenv.Env, n int) {
	t.Helper()
	root := env.MustResolve("").Ino
	attr, fh, err := env.FS.Create(root, "empty.bin", syscall.O_WRONLY|syscall.O_TRUNC)
	if err != nil {
		t.Fatalf("creating an empty file: %v", err)
	}
	if _, err := env.FS.Write(context.Background(), fh, 0, make([]byte, n)); err != syscall.EROFS {
		t.Fatalf("a write of content got %v, want EROFS", err)
	}
	env.FS.Release(fh)
	if attr.Size != 0 {
		t.Fatalf("the empty file has %d bytes", attr.Size)
	}
	mkdir(t, env, "", "ro")
	rename(t, env, "", "empty.bin", "ro", "empty.bin")
	if err := env.FS.Unlink(env.MustResolve("ro").Ino, "empty.bin"); err != nil {
		t.Fatalf("unlink: %v", err)
	}
}

func TestOwnCountRefusesWhatDriveCouldNotTake(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Tune = func(o *core.Options) { o.DailyUpload = 1 << 20 }
	env.Start()
	writeFile(t, env, "", "a.bin", testenv.RandomBytes(600<<10, 71))
	uploadAll(t, env)

	_, fh := createFile(t, env, "", "b.bin")
	if _, err := env.FS.Write(context.Background(), fh, 0, testenv.RandomBytes(300<<10, 72)); err != nil {
		t.Fatalf("a write that fits today's limit: %v", err)
	}
	if _, err := env.FS.Write(context.Background(), fh, 300<<10, testenv.RandomBytes(200<<10, 73)); err != syscall.EROFS {
		t.Fatalf("a write past today's limit got %v, want EROFS", err)
	}
	env.FS.Release(fh)
	if len(env.Logs("what the daily upload limit leaves")) == 0 {
		t.Fatal("the reason is not in the log")
	}
	requireReadOnly(t, env, 200<<10)

	env.Clock.Advance(25 * time.Hour)
	writeFile(t, env, "", "c.bin", testenv.RandomBytes(500<<10, 74))
}

func TestFreeSpaceInDriveRefusesWrites(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	env.Emu.SetQuota(10<<20, 9<<20)
	if err := env.FS.RefreshQuota(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, fh := createFile(t, env, "", "a.bin")
	defer env.FS.Release(fh)
	if _, err := env.FS.Write(context.Background(), fh, 0, testenv.RandomBytes(512<<10, 75)); err != nil {
		t.Fatalf("a write that fits the free space: %v", err)
	}
	if _, err := env.FS.Write(context.Background(), fh, 512<<10, testenv.RandomBytes(600<<10, 76)); err != syscall.EROFS {
		t.Fatalf("a write past the free space got %v, want EROFS", err)
	}
	if len(env.Logs("free space in Drive")) == 0 {
		t.Fatal("the reason is not in the log")
	}
	requireReadOnly(t, env, 600<<10)
}

func TestDriveRefusalOutlivesARestart(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	writeFile(t, env, "", "a.bin", []byte("first"))
	env.Emu.SetUploadLimitReached(true)
	env.Clock.Advance(20 * time.Minute)
	env.FS.SyncOnce(context.Background())
	env.Restart()
	requireReadOnly(t, env, 1)
}

func TestUploadLimitWaitsUpToAnHour(t *testing.T) {
	t.Parallel()
	for n := 1; n < 20; n++ {
		if d := core.LimitBackoff(n); d < 48*time.Second || d > time.Hour {
			t.Fatalf("attempt %d waits %v", n, d)
		}
	}
	if d := core.LimitBackoff(15); d < 48*time.Minute {
		t.Fatalf("a long refused upload waits %v, want about an hour", d)
	}
	requireLimitBackoffDoubles(t)
}

func TestUploadsGoAsSoonAsTheNetworkIsBack(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	content := []byte("cached")
	env.Emu.WriteFile(env.Emu.Root(), "cached.bin", content)
	env.Start()
	readAll(t, env, "cached.bin")

	env.Emu.SetOffline(true)
	if got := readAll(t, env, "cached.bin"); string(got) != "cached" {
		t.Fatalf("cached content reads %q with Drive down", got)
	}
	writeFile(t, env, "", "a.bin", []byte("written offline"))
	env.Clock.Advance(20 * time.Minute)
	env.FS.SyncOnce(context.Background())
	it := item(t, env, "a.bin")
	if !it.Dirty || it.Attempts == 0 || !it.NextTry.After(env.Clock.Now()) {
		t.Fatalf("an upload with Drive down left dirty=%v attempts=%d", it.Dirty, it.Attempts)
	}

	env.Emu.SetOffline(false)
	poll(t, env)
	waitUntil(t, func() bool { return !item(t, env, "a.bin").NextTry.After(env.Clock.Now()) })
	env.FS.SyncOnce(context.Background())
	if f := inDrive(t, env, "a.bin"); string(f.Content) != "written offline" {
		t.Fatalf("Drive has %q", f.Content)
	}
}

func TestLimitOnAChunkOfALargeUploadTurnsWritesAway(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		refuse func(e *emulator.Emulator, on bool)
		logged string
	}{
		"daily limit":  {(*emulator.Emulator).SetUploadLimitReached, "daily upload limit"},
		"storage full": {(*emulator.Emulator).SetStorageFull, "storage is full"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			env := testenv.New(t)
			content := refuseChunk(t, env, func(on bool) { c.refuse(env.Emu, on) })
			requireChunkRefusalShown(t, env, c.logged)
			c.refuse(env.Emu, false)
			requireUploadResumesAfterRefusal(t, env, content)
			writeFile(t, env, "", "more.bin", []byte("more"))
		})
	}
}

func TestUploadOfNoBytesLeavesTheRefusal(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	writeFile(t, env, "", "a.bin", []byte("first"))
	env.Emu.SetUploadLimitReached(true)
	env.Clock.Advance(20 * time.Minute)
	env.FS.SyncOnce(context.Background())
	made := 0

	write := func() error {
		made++
		_, fh := createFile(t, env, "", fmt.Sprintf("new%d.bin", made))
		defer env.FS.Release(fh)
		_, err := env.FS.Write(context.Background(), fh, 0, []byte("x"))
		return err
	}
	testenv.RequireErrno(t, write(), syscall.EROFS)
	env.FS.AcceptedBytes(0)
	testenv.RequireErrno(t, write(), syscall.EROFS)
	env.FS.AcceptedBytes(1)
	if err := write(); err != nil {
		t.Fatalf("after an upload of content went through a write got %v", err)
	}
}
