package core_test

import (
	"bytes"
	"context"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestEmptyFileWithAnOpenWriterWaitsTenMinutes(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	ctx := context.Background()
	env.Emu.ResetRequests()
	_, fh := createFile(t, env, "", "empty.txt")
	env.Clock.Advance(10*time.Minute - time.Second)
	env.FS.SyncOnce(ctx)
	if got := env.Emu.Requests(); len(got) != 0 {
		t.Fatalf("Drive got %d requests for an empty open file within ten minutes, the first %+v", len(got), got[0])
	}
	env.Clock.Advance(2 * time.Second)
	env.FS.SyncOnce(ctx)
	if f := inDrive(t, env, "empty.txt"); len(f.Content) != 0 {
		t.Fatalf("the empty file went up with %d bytes", len(f.Content))
	}
	env.FS.Release(fh)

	_, fh = createFile(t, env, "", "late.txt")
	env.Clock.Advance(8 * time.Minute)
	write(t, env, fh, 0, []byte("late"))
	env.Clock.Advance(12 * time.Hour)
	env.FS.SyncOnce(ctx)
	if n := len(env.Emu.Children(env.Emu.Root())); n != 1 {
		t.Fatalf("Drive holds %d items: the file written late went before it was flushed", n)
	}
	if err := env.FS.Fsync(fh); err != nil {
		t.Fatal(err)
	}
	env.FS.SyncOnce(ctx)
	if f := inDrive(t, env, "late.txt"); string(f.Content) != "late" {
		t.Fatalf("the file written late went up with %q", f.Content)
	}
	env.FS.Release(fh)

	_, fh = createFile(t, env, "", "closed-empty.txt")
	env.FS.Release(fh)
	env.FS.SyncOnce(ctx)
	inDrive(t, env, "closed-empty.txt")
}

func TestRepeatedClosesWithinTwoSecondsAreOneUpload(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	ctx := context.Background()
	rewrite := func(content string) {
		fh := openWriter(t, env, "f.txt", syscall.O_TRUNC)
		write(t, env, fh, 0, []byte(content))
		env.FS.Release(fh)
	}

	writeFile(t, env, "", "f.txt", []byte("one"))
	rewrite("two")
	env.FS.SyncOnce(ctx)
	if c, _ := uploads(env); c != 1 || string(inDrive(t, env, "f.txt").Content) != "two" {
		t.Fatalf("%d uploads, Drive holds %q", c, inDrive(t, env, "f.txt").Content)
	}
	env.Emu.ResetRequests()
	env.Clock.Advance(time.Second)
	rewrite("three")
	env.FS.SyncOnce(ctx)
	env.Clock.Advance(900 * time.Millisecond)
	rewrite("four")
	env.FS.SyncOnce(ctx)
	if c, _ := uploads(env); c != 0 {
		t.Fatalf("%d uploads within two seconds of the one before", c)
	}
	env.Clock.Advance(200 * time.Millisecond)
	env.FS.SyncOnce(ctx)
	if c, _ := uploads(env); c != 1 || string(inDrive(t, env, "f.txt").Content) != "four" {
		t.Fatalf("%d uploads after the two seconds, Drive holds %q", c, inDrive(t, env, "f.txt").Content)
	}
}

func rewriteFile(t *testing.T, env *testenv.Env, content []byte) {
	t.Helper()
	fh := openWriter(t, env, "f.bin", syscall.O_TRUNC)
	write(t, env, fh, 0, content)
	env.FS.Release(fh)
}

func requireSameContentKeptHere(t *testing.T, env *testenv.Env, id string, firstModified, written time.Time,
	content []byte) {
	t.Helper()

	if c, m := uploads(env); c != 0 || m != 1 {
		t.Fatalf("the same content took %d uploads of content and %d requests of metadata, want 0 and 1", c, m)
	}
	if it := item(t, env, "f.bin"); it.AheadOfDrive() {
		t.Fatalf("the file with the content Drive holds still waits for Drive: %+v", it)
	}
	if f, _ := env.Emu.Get(id); !f.Modified.Equal(written) || !written.After(firstModified) ||
		len(env.Emu.Revisions(id)) != 1 {
		t.Fatalf("Drive holds the time %v in %d revisions, want the time of the write, %v, in one", f.Modified,
			len(env.Emu.Revisions(id)), written)
	}
	if a := env.MustResolve("f.bin"); !a.Mtime.Equal(written) {
		t.Fatalf("the mount shows the time %v, the file was written at %v", a.Mtime, written)
	}
	if got := readAll(t, env, "f.bin"); !bytes.Equal(got, content) {
		t.Fatal("the file reads different")
	}
}

func requireOtherContentGoesUp(t *testing.T, env *testenv.Env, id string) []byte {
	t.Helper()

	other := testenv.RandomBytes(300000, 32)
	rewriteFile(t, env, other)
	env.FS.SyncOnce(context.Background())
	if f, _ := env.Emu.Get(id); !bytes.Equal(f.Content, other) || len(env.Emu.Revisions(id)) != 2 {
		t.Fatalf("other content: Drive holds %d revisions", len(env.Emu.Revisions(id)))
	}
	return other
}

func requireSameContentWaitsTenMinutes(t *testing.T, env *testenv.Env, id string, other []byte) {
	t.Helper()
	ctx := context.Background()

	env.Clock.Advance(10*time.Minute - time.Second)
	rewriteFile(t, env, other)
	env.FS.SyncOnce(ctx)
	if got := len(env.Emu.Revisions(id)); got != 2 || item(t, env, "f.bin").AheadOfDrive() {
		t.Fatalf("the same content just within ten minutes: %d revisions, want 2", got)
	}
	env.Emu.ResetRequests()
	env.Clock.Advance(2 * time.Second)
	rewriteFile(t, env, other)
	env.FS.SyncOnce(ctx)
	if c, _ := uploads(env); c != 1 || item(t, env, "f.bin").AheadOfDrive() {
		t.Fatalf("the same content after ten minutes: %d uploads, want 1", c)
	}
}

func TestSameContentIsNotUploadedAgainWithinTenMinutes(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	content := testenv.RandomBytes(300000, 31)
	ctx := context.Background()
	writeFile(t, env, "", "f.bin", content)
	env.FS.SyncOnce(ctx)
	id := item(t, env, "f.bin").ID
	first, _ := env.Emu.Get(id)
	env.Emu.ResetRequests()
	env.Clock.Advance(5 * time.Minute)
	rewriteFile(t, env, content)
	written := env.MustResolve("f.bin").Mtime
	env.FS.SyncOnce(ctx)
	requireSameContentKeptHere(t, env, id, first.Modified, written, content)
	other := requireOtherContentGoesUp(t, env, id)
	requireSameContentWaitsTenMinutes(t, env, id, other)
}

func TestDamagedUploadIsRetried(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	content := testenv.RandomBytes(100000, 30)
	writeFile(t, env, "", "f.bin", content)
	env.Emu.CorruptUploads(1)
	env.Clock.Advance(time.Minute)
	env.FS.SyncOnce(context.Background())
	it := item(t, env, "f.bin")
	if !it.Dirty || it.Attempts != 1 {
		t.Fatalf("after a damaged upload: dirty %v, attempts %d", it.Dirty, it.Attempts)
	}
	uploadAll(t, env)
	if f := inDrive(t, env, "f.bin"); md5Of(f.Content) != md5Of(content) {
		t.Fatal("Drive kept the damaged content")
	}
	if it := item(t, env, "f.bin"); it.Dirty || it.MD5 != md5Of(content) {
		t.Fatalf("after the retry: %+v", it)
	}
}

func TestWriteDuringUploadIsNotLost(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	writeFile(t, env, "", "f.bin", []byte("first part, "))
	env.Clock.Advance(time.Minute)
	release := env.Emu.HoldUploads()
	done := make(chan struct{})
	go func() {
		env.FS.SyncOnce(context.Background())
		close(done)
	}()
	waitUntil(t, func() bool { return env.Emu.UploadsWaiting() == 1 })

	fh, err := env.FS.Open(context.Background(), env.MustResolve("f.bin").Ino, syscall.O_WRONLY)
	if err != nil {
		t.Fatal(err)
	}
	write(t, env, fh, 12, []byte("second part"))
	env.FS.Release(fh)
	release()
	<-done
	if it := item(t, env, "f.bin"); !it.Dirty {
		t.Fatal("the file is clean although written during its upload")
	}
	uploadAll(t, env)
	if f := inDrive(t, env, "f.bin"); string(f.Content) != "first part, second part" {
		t.Fatalf("Drive has %q", f.Content)
	}
}
