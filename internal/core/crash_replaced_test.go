package core_test

import (
	"context"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestRefusalOfUploadsEndsWhenNothingIsLeftToUpload(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	env.Emu.SetStorageFull(true)
	writeFile(t, env, "", "big.bin", testenv.RandomBytes(4096, 720))
	env.Clock.Advance(20 * time.Minute)
	env.FS.SyncOnce(context.Background())
	if _, fh, err := env.FS.Create(env.MustResolve("").Ino, "more.bin", syscall.O_WRONLY); err == nil {
		defer env.FS.Release(fh)
		if _, err := env.FS.Write(context.Background(), fh, 0, []byte("x")); err != syscall.EROFS {
			t.Fatalf("scenario not built: a write after the refusal got %v", err)
		}
	}
	for _, name := range env.Names("") {
		if err := env.FS.Unlink(env.MustResolve("").Ino, name); err != nil {
			t.Fatal(err)
		}
	}
	env.Emu.SetStorageFull(false)

	rounds(env, 2)
	writeFile(t, env, "", "after.bin", []byte("written after the room came back"))
	uploadAll(t, env)
	if f := inDrive(t, env, "after.bin"); string(f.Content) != "written after the room came back" {
		t.Fatal("the file written after the refusal is not in Drive")
	}
}

func TestReplacedFileStaysInDriveUntilTheNewContentIsThere(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	old := env.Emu.WriteFile(env.Emu.Root(), "doc.txt", []byte("old"))
	env.Start()
	defer env.FS.Release(openWriter(t, env, "doc.txt", 0))
	writeFile(t, env, "", "doc.tmp", []byte("new"))
	rename(t, env, "", "doc.tmp", "", "doc.txt")

	env.Emu.SetStorageFull(true)
	rounds(env, 3)
	if f, _ := env.Emu.Get(old); f.Trashed {
		t.Fatal("the replaced file is in Drive's trash while the new content is not in Drive")
	}
	env.Emu.SetStorageFull(false)
	uploadAll(t, env)
	if f, _ := env.Emu.Get(old); !f.Trashed {
		t.Fatal("the replaced file is not in Drive's trash after the new content went up")
	}
	if f := inDrive(t, env, "doc.txt"); string(f.Content) != "new" {
		t.Fatalf("Drive holds %q under the name", f.Content)
	}
}

func TestReplacedFileComesBackWhenDriveNeverTakesTheNewContent(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	dir := env.Emu.Mkdir(env.Emu.Root(), "d")
	old := env.Emu.WriteFile(dir, "doc.txt", []byte("old"))
	env.Start()

	defer env.FS.Release(openWriter(t, env, "d/doc.txt", 0))
	writeFile(t, env, "d", "doc.tmp", []byte("new"))
	rename(t, env, "d", "doc.tmp", "d", "doc.txt")

	env.Emu.Update(dir, func(f *emulator.File) { f.CannotAddChildren = true })
	rounds(env, 6)
	if f, _ := env.Emu.Get(old); f.Trashed {
		t.Fatal("the replaced file is in Drive's trash though the new content never got there")
	}
	if got := readAll(t, env, "d/doc.txt"); string(got) != "old" {
		t.Fatalf("under the name the mount shows %q, want the file that was replaced", got)
	}
	requireShelved(t, env, "d", "doc", ".txt", []byte("new"))
}

func TestReplacedTwiceInARowStaysInDriveUntilTheLastContentIsThere(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	old := env.Emu.WriteFile(env.Emu.Root(), "doc.txt", []byte("old"))
	env.Start()
	defer env.FS.Release(openWriter(t, env, "doc.txt", 0))
	writeFile(t, env, "", "doc.tmp1", []byte("first"))
	rename(t, env, "", "doc.tmp1", "", "doc.txt")
	writeFile(t, env, "", "doc.tmp2", []byte("second"))
	rename(t, env, "", "doc.tmp2", "", "doc.txt")
	env.Emu.SetStorageFull(true)
	rounds(env, 3)
	if f, _ := env.Emu.Get(old); f.Trashed {
		t.Fatal("the replaced file is in Drive's trash while no new content is in Drive")
	}
	env.Emu.SetStorageFull(false)
	uploadAll(t, env)
	if f, _ := env.Emu.Get(old); !f.Trashed {
		t.Fatal("the replaced file is not in Drive's trash after the new content went up")
	}
	if f := inDrive(t, env, "doc.txt"); string(f.Content) != "second" {
		t.Fatalf("Drive holds %q under the name", f.Content)
	}
}
