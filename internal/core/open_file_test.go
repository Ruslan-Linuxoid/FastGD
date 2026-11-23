package core_test

import (
	"context"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestCreatedFileWaitsForItsFirstWrite(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()

	_, fh, err := env.FS.Create(env.MustResolve("").Ino, "copy.bin", syscall.O_WRONLY|syscall.O_EXCL)
	if err != nil {
		t.Fatal(err)
	}
	env.Clock.Advance(time.Minute)
	env.FS.SyncOnce(context.Background())
	if n := len(env.Emu.Children(env.Emu.Root())); n != 0 {
		t.Fatal("an open file was uploaded empty before its first write")
	}
	write(t, env, fh, 0, []byte("copied"))
	env.FS.Release(fh)
	uploadAll(t, env)
	if f := inDrive(t, env, "copy.bin"); string(f.Content) != "copied" {
		t.Fatalf("Drive has %q", f.Content)
	}
}

func TestCreateForReadingAndWriting(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	_, fh, err := env.FS.Create(env.MustResolve("").Ino, "db.sqlite", syscall.O_RDWR)
	if err != nil {
		t.Fatal(err)
	}
	write(t, env, fh, 0, []byte("page"))
	if got := mustRead(t, env, fh, 0, 4); string(got) != "page" {
		t.Fatalf("read through the same handle: %q", got)
	}
	env.FS.Release(fh)
}

func requireOpenFileHeldBackUntilFlush(t *testing.T, flush func(env *testenv.Env, fh uint64) error) {
	t.Helper()
	env := testenv.New(t)
	env.Start()
	_, fh := createFile(t, env, "", "log.txt")
	defer env.FS.Release(fh)
	write(t, env, fh, 0, []byte("one "))

	for range 6 {
		env.Clock.Advance(12 * time.Hour)
		env.FS.SyncOnce(context.Background())
	}
	if it := item(t, env, "log.txt"); !it.Dirty {
		t.Fatal("a file that is written into and not flushed went to Drive")
	}
	if n := len(env.Emu.All()); n != 0 {
		t.Fatalf("Drive holds %d items, want none", n)
	}
	if err := flush(env, fh); err != nil {
		t.Fatal(err)
	}

	env.Clock.Advance(3 * time.Second)
	env.FS.SyncOnce(context.Background())
	if it := item(t, env, "log.txt"); it.Dirty {
		t.Fatal("the flushed file did not go to Drive")
	}
	if f := inDrive(t, env, "log.txt"); string(f.Content) != "one " {
		t.Fatalf("Drive has %q", f.Content)
	}

	write(t, env, fh, 4, []byte("two"))
	env.Clock.Advance(12 * time.Hour)
	env.FS.SyncOnce(context.Background())
	if f := inDrive(t, env, "log.txt"); string(f.Content) != "one " {
		t.Fatalf("after a write that was not flushed Drive has %q", f.Content)
	}
}

func TestOpenFileGoesToDriveOnlyAfterAFlush(t *testing.T) {
	t.Parallel()
	for name, flush := range map[string]func(env *testenv.Env, fh uint64) error{
		"fsync": func(env *testenv.Env, fh uint64) error { return env.FS.Fsync(fh) },
		"a read": func(env *testenv.Env, fh uint64) error {
			_, err := env.FS.Read(context.Background(), fh, 0, make([]byte, 2))
			return err
		},
		"a read through another handle": func(env *testenv.Env, _ uint64) error {
			fh, err := env.FS.Open(context.Background(), env.MustResolve("log.txt").Ino, syscall.O_RDONLY)
			if err != nil {
				return err
			}
			defer env.FS.Release(fh)
			_, err = env.FS.Read(context.Background(), fh, 0, make([]byte, 2))
			return err
		},
		"a truncation": func(env *testenv.Env, _ uint64) error {
			return env.FS.Truncate(context.Background(), env.MustResolve("log.txt").Ino, 4)
		},
		"the close of another writer": func(env *testenv.Env, _ uint64) error {
			fh, err := env.FS.Open(context.Background(), env.MustResolve("log.txt").Ino, syscall.O_WRONLY)
			if err != nil {
				return err
			}
			env.FS.Release(fh)
			return nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			requireOpenFileHeldBackUntilFlush(t, flush)
		})
	}
}

func TestWriteAfterAnUploadOfAnOpenFileMarksDirtyAgain(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	_, fh := createFile(t, env, "", "log.txt")
	write(t, env, fh, 0, []byte("one "))
	if err := env.FS.Fsync(fh); err != nil {
		t.Fatal(err)
	}
	env.FS.SyncOnce(context.Background())
	if it := item(t, env, "log.txt"); it.Dirty {
		t.Fatal("an open file that was synced was not uploaded")
	}
	write(t, env, fh, 4, []byte("two"))
	if it := item(t, env, "log.txt"); !it.Dirty {
		t.Fatal("a write to the clean file did not mark it dirty in the database first")
	}
	env.FS.Release(fh)
	uploadAll(t, env)
	if f := inDrive(t, env, "log.txt"); string(f.Content) != "one two" {
		t.Fatalf("Drive has %q", f.Content)
	}
}

func TestTruncateOfAnUploadedFileReachesDrive(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	writeFile(t, env, "", "f.txt", []byte("long content"))
	uploadAll(t, env)
	if err := env.FS.Truncate(context.Background(), env.MustResolve("f.txt").Ino, 4); err != nil {
		t.Fatal(err)
	}
	uploadAll(t, env)
	if f := inDrive(t, env, "f.txt"); string(f.Content) != "long" {
		t.Fatalf("Drive has %q", f.Content)
	}
}

func TestFsyncSavesTheFileState(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	_, fh := createFile(t, env, "", "f.txt")
	write(t, env, fh, 0, []byte("12345"))
	if err := env.FS.Fsync(fh); err != nil {
		t.Fatal(err)
	}
	if it := item(t, env, "f.txt"); it.Size != 5 || it.Gen == 0 {
		t.Fatalf("after fsync the database has size %d, generation %d", it.Size, it.Gen)
	}
	env.FS.Release(fh)
}

func TestFolderCreateRetryMakesOneFolder(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	mkdir(t, env, "", "dir")
	env.Emu.LoseUploadAnswers(1)
	uploadAll(t, env)
	inDrive(t, env, "dir")
	if it := item(t, env, "dir"); !it.RExists || it.Attempts != 0 {
		t.Fatalf("after the retry: %+v", it)
	}
}
