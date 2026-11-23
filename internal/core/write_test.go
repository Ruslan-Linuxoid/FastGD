package core_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestWrittenFileReadsBackWithDriveDown(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	env.Emu.SetOffline(true)
	mkdir(t, env, "", "new")
	_, fh := createFile(t, env, "new", "f.txt")
	write(t, env, fh, 0, []byte("hello "))
	write(t, env, fh, 6, []byte("world"))
	if err := env.FS.Fsync(fh); err != nil {
		t.Fatal(err)
	}
	env.FS.Release(fh)

	if got := readAll(t, env, "new/f.txt"); string(got) != "hello world" {
		t.Fatalf("read back %q", got)
	}
	if got := env.Names("new"); !slices.Equal(got, []string{"f.txt"}) {
		t.Fatalf("new lists %q", got)
	}
}

func TestWritesNeedNoTransactionEach(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	_, fh := createFile(t, env, "", "f.txt")
	if it := item(t, env, "f.txt"); !it.Dirty {
		t.Fatal("a new file is not dirty in the database before its first write")
	}
	write(t, env, fh, 0, []byte("abc"))
	write(t, env, fh, 3, []byte("def"))
	if it := item(t, env, "f.txt"); it.Size != 0 {
		t.Fatalf("a write reached the database: size %d", it.Size)
	}
	if a := env.MustResolve("f.txt"); a.Size != 6 {
		t.Fatalf("stat shows size %d while writing", a.Size)
	}
	env.FS.Release(fh)
	if it := item(t, env, "f.txt"); it.Size != 6 {
		t.Fatalf("close left size %d in the database", it.Size)
	}
}

func TestDirtyFileSurvivesRestart(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	_, fh := createFile(t, env, "", "f.txt")
	write(t, env, fh, 0, []byte("not yet closed"))

	env.Restart()
	if got := readAll(t, env, "f.txt"); string(got) != "not yet closed" {
		t.Fatalf("after restart reads %q", got)
	}
	uploadAll(t, env)
	if f := inDrive(t, env, "f.txt"); string(f.Content) != "not yet closed" {
		t.Fatalf("Drive has %q", f.Content)
	}
}

func TestLostAnswerOfACreateMakesNoSecondFile(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	ino, fh := createFile(t, env, "", "f.txt")
	write(t, env, fh, 0, []byte("x"))
	env.FS.Release(fh)

	env.Emu.LoseUploadAnswers(1)
	uploadAll(t, env)
	it, _ := env.FS.Store().ByIno(meta.Ino(ino))
	if f := inDrive(t, env, "f.txt"); f.ID != it.ID || string(f.Content) != "x" {
		t.Fatalf("Drive has %s with %q, want %s", f.ID, f.Content, it.ID)
	}
	made := 0
	for _, f := range env.Emu.All() {
		if f.Name == "f.txt" {
			made++
		}
	}
	if made != 1 {
		t.Fatalf("Drive holds %d files of the name, want one", made)
	}
	if it.Dirty {
		t.Fatal("file still dirty after the retry")
	}
}

func TestUploadGoesAtOnceAfterCloseAndAfterASyncWhileOpen(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	ctx := context.Background()
	writeFile(t, env, "", "closed.txt", []byte("c"))
	env.FS.SyncOnce(ctx)
	inDrive(t, env, "closed.txt")

	_, fh := createFile(t, env, "", "open.txt")
	env.Clock.Advance(time.Minute)
	write(t, env, fh, 0, []byte("o"))
	env.Clock.Advance(12 * time.Hour)
	env.FS.SyncOnce(ctx)
	if len(env.Emu.Children(env.Emu.Root())) != 1 {
		t.Fatal("an open file was uploaded with what was written into it not flushed")
	}
	if err := env.FS.Fsync(fh); err != nil {
		t.Fatal(err)
	}
	env.FS.SyncOnce(ctx)
	inDrive(t, env, "open.txt")
	env.FS.Release(fh)
}

func TestFileLeftOpenByAKilledRunGoesUpAtTheStart(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	_, fh := createFile(t, env, "", "open.txt")
	write(t, env, fh, 0, []byte("written, never closed"))
	if err := env.FS.Fsync(fh); err != nil {
		t.Fatal(err)
	}
	env.Restart()
	env.FS.SyncOnce(context.Background())
	if f := inDrive(t, env, "open.txt"); string(f.Content) != "written, never closed" {
		t.Fatalf("Drive holds %q", f.Content)
	}
}
