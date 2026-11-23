package core_test

import (
	"bytes"
	"context"
	"slices"
	"syscall"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestChangedFileReadsTheNewVersionWhole(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	emu := env.Emu
	oldBytes := testenv.RandomBytes(2*big, 20)
	id := emu.WriteFile(emu.Root(), "f.bin", oldBytes)
	env.Start()
	readAll(t, env, "f.bin")

	newBytes := testenv.RandomBytes(3*big+7, 21)
	emu.Update(id, func(f *emulator.File) { f.Content = newBytes })
	poll(t, env)
	if got := readAll(t, env, "f.bin"); !bytes.Equal(got, newBytes) {
		t.Fatal("the changed file does not read as its new version")
	}
}

func TestOpenHandleKeepsItsVersion(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	emu := env.Emu
	oldBytes := testenv.RandomBytes(2*big, 22)
	id := emu.WriteFile(emu.Root(), "f.bin", oldBytes)
	env.Start()
	old := openFile(t, env, "f.bin")
	mustRead(t, env, old, 0, 4096)

	newBytes := testenv.RandomBytes(50, 23)
	emu.Update(id, func(f *emulator.File) { f.Content = newBytes })
	poll(t, env)

	if got := mustRead(t, env, old, 100, 100); !bytes.Equal(got, oldBytes[100:200]) {
		t.Fatal("an old handle does not read the version it opened from the cache")
	}
	_, err := env.FS.Read(context.Background(), old, big+5, make([]byte, 100))
	testenv.RequireErrno(t, err, syscall.EIO)

	fresh := openFile(t, env, "f.bin")
	if got := mustRead(t, env, fresh, 0, 100); !bytes.Equal(got, newBytes) {
		t.Fatal("a new handle does not read the new version")
	}
}

func TestOpenHandleOfARemovedFileReadsWhatWasCached(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	emu := env.Emu
	content := testenv.RandomBytes(big+10, 26)
	id := emu.WriteFile(emu.Root(), "f.bin", content)
	env.Start()
	fh := openFile(t, env, "f.bin")
	mustRead(t, env, fh, 0, 10)

	emu.Delete(id)
	poll(t, env)
	if _, err := env.Resolve("f.bin"); err != syscall.ENOENT {
		t.Fatalf("removed file resolves: %v", err)
	}
	if got := mustRead(t, env, fh, 20, 10); !bytes.Equal(got, content[20:30]) {
		t.Fatal("the open handle lost the cached content of a removed file")
	}
	_, err := env.FS.Read(context.Background(), fh, big+1, make([]byte, 5))
	testenv.RequireErrno(t, err, syscall.EIO)
}

func TestRenameInDriveKeepsTheCachedContent(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	content := testenv.RandomBytes(40<<10, 540)
	id := env.Emu.WriteFile(env.Emu.Root(), "old.txt", content)
	env.Start()
	readClosed(t, env, "old.txt")
	ino := env.MustResolve("old.txt").Ino
	rec := &recorder{}
	env.FS.SetNotifier(rec)
	env.Emu.Update(id, func(f *emulator.File) { f.Name = "new.txt" })
	poll(t, env)
	if !slices.Contains(rec.inodes, fmtInode(ino, false)) || slices.Contains(rec.inodes, fmtInode(ino, true)) {
		t.Fatalf("the rename told the kernel %q, want the attributes alone", rec.inodes)
	}
	env.Emu.SetOffline(true)
	if got := readAll(t, env, "new.txt"); !bytes.Equal(got, content) {
		t.Fatal("after the rename the file does not read from the cache")
	}
}
