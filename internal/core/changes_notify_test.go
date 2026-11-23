package core_test

import (
	"bytes"
	"fmt"
	"slices"
	"syscall"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestChangesNotifyTheKernel(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	emu := env.Emu
	dir := emu.Mkdir(emu.Root(), "dir")
	edited := emu.WriteFile(dir, "edited.txt", []byte("1"))
	renamed := emu.WriteFile(dir, "before.txt", []byte("2"))
	env.Start()
	rec := &recorder{}
	env.FS.SetNotifier(rec)
	dirIno := env.MustResolve("dir").Ino
	editedIno := env.MustResolve("dir/edited.txt").Ino
	renamedIno := env.MustResolve("dir/before.txt").Ino

	emu.Update(edited, func(f *emulator.File) { f.Content = []byte("11") })
	emu.Update(renamed, func(f *emulator.File) { f.Name = "after.txt" })
	poll(t, env)

	for _, want := range []string{
		fmt.Sprintf("%d/before.txt", dirIno),
		fmt.Sprintf("%d/after.txt", dirIno),
		fmt.Sprintf("%d/edited.txt", dirIno),
	} {
		if !slices.Contains(rec.entries, want) {
			t.Errorf("no entry notification %s in %q", want, rec.entries)
		}
	}
	for _, want := range []string{
		fmt.Sprintf("%d content=true", editedIno),
		fmt.Sprintf("%d content=false", renamedIno),
	} {
		if !slices.Contains(rec.inodes, want) {
			t.Errorf("no inode notification %s in %q", want, rec.inodes)
		}
	}
}

func TestChangeLeavesSimilarFolderAlone(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	emu := env.Emu
	ab := emu.Mkdir(emu.Root(), "a_b")
	axb := emu.Mkdir(emu.Root(), "aXb")
	changed := emu.WriteFile(ab, "f.bin", testenv.RandomBytes(1000, 24))
	neighbour := testenv.RandomBytes(1000, 25)
	emu.WriteFile(axb, "f.bin", neighbour)
	env.Start()
	readAll(t, env, "a_b/f.bin")
	readAll(t, env, "aXb/f.bin")
	rec := &recorder{}
	env.FS.SetNotifier(rec)
	axbIno := env.MustResolve("aXb").Ino
	neighbourIno := env.MustResolve("aXb/f.bin").Ino

	emu.Update(changed, func(f *emulator.File) { f.Content = []byte("new") })
	poll(t, env)
	emu.SetOffline(true)
	if got := readAll(t, env, "aXb/f.bin"); !bytes.Equal(got, neighbour) {
		t.Fatal("the neighbour folder's cached file is gone")
	}
	for _, e := range rec.entries {
		if e == fmt.Sprintf("%d/f.bin", axbIno) {
			t.Fatalf("the neighbour folder got notification %s", e)
		}
	}
	for _, i := range rec.inodes {
		if i == fmt.Sprintf("%d content=true", neighbourIno) || i == fmt.Sprintf("%d content=false", neighbourIno) {
			t.Fatalf("the neighbour file got notification %s", i)
		}
	}
}

func TestChildrenOfAFolderGoneFromDriveAreOutOfSight(t *testing.T) {
	t.Parallel()
	for name, gone := range map[string]func(env *testenv.Env, id string){
		"trashed": func(env *testenv.Env, id string) { env.Emu.Update(id, func(f *emulator.File) { f.Trashed = true }) },
		"removed": func(env *testenv.Env, id string) { env.Emu.Delete(id) },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			env := testenv.New(t)
			docs := env.Emu.Mkdir(env.Emu.Root(), "Docs")
			sub := env.Emu.Mkdir(docs, "sub")
			env.Emu.WriteFile(sub, "f.txt", []byte("inside"))
			env.Emu.WriteFile(env.Emu.Root(), "beside.txt", []byte("beside"))
			env.Start()
			readClosed(t, env, "Docs/sub/f.txt")
			gone(env, docs)
			poll(t, env)
			if got := env.Names(""); !slices.Equal(got, []string{"beside.txt"}) {
				t.Fatalf("after the folder went the root lists %q", got)
			}
			for _, p := range []string{"Docs", "Docs/sub", "Docs/sub/f.txt"} {
				_, err := env.Resolve(p)
				testenv.RequireErrno(t, err, syscall.ENOENT)
			}
		})
	}
}

func TestMoveTrashAndRemovalInDriveNotifyTheKernel(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	emu := env.Emu
	from := emu.Mkdir(emu.Root(), "from")
	to := emu.Mkdir(emu.Root(), "to")
	moved := emu.WriteFile(from, "moved.txt", []byte("1"))
	trashed := emu.WriteFile(from, "trashed.txt", []byte("2"))
	removed := emu.WriteFile(from, "removed.txt", []byte("3"))
	env.Start()
	fromIno, toIno := env.MustResolve("from").Ino, env.MustResolve("to").Ino
	removedIno := env.MustResolve("from/removed.txt").Ino
	rec := &recorder{}
	env.FS.SetNotifier(rec)

	emu.Update(moved, func(f *emulator.File) { f.Parents = []string{to} })
	emu.Update(trashed, func(f *emulator.File) { f.Trashed = true })
	emu.Delete(removed)
	poll(t, env)
	if !heard(rec, fromIno, "moved.txt", "trashed.txt", "removed.txt") || !heard(rec, toIno, "moved.txt") {
		t.Fatalf("the kernel heard of the names %q", rec.entries)
	}
	if !slices.Contains(rec.inodes, fmtInode(removedIno, true)) {
		t.Fatalf("the kernel is not told to drop the pages of the removed file: told %q", rec.inodes)
	}
	if got := env.Names("from"); len(got) != 0 {
		t.Fatalf("the folder the three left lists %q", got)
	}
	if got := env.Names("to"); !slices.Equal(got, []string{"moved.txt"}) {
		t.Fatalf("the folder the file moved to lists %q", got)
	}
}
