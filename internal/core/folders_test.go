package core_test

import (
	"errors"
	"syscall"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestAPathNamesItsFolder(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	work := env.Emu.Mkdir(env.Emu.Root(), "Work")
	docs := env.Emu.Mkdir(work, "Docs")
	env.Emu.WriteFile(docs, "a.txt", []byte("a"))
	env.Start()
	for path, want := range map[string]string{"Work/Docs": docs, "/Work/": work, "": env.Emu.Root(),
		meta.LostName: core.LostFolder} {
		if id, err := env.FS.FolderAt(path); err != nil || id != want {
			t.Fatalf("%q names %q, %v; want %q", path, id, err, want)
		}
	}
	if _, err := env.FS.FolderAt("Work/Docs/a.txt"); !errors.Is(err, core.ErrNotFolder) {
		t.Fatalf("a file names a folder: %v", err)
	}
	if _, err := env.FS.FolderAt("Work/None"); !errors.Is(err, syscall.ENOENT) {
		t.Fatalf("a path the mount does not show: %v", err)
	}
	if _, err := env.FS.Mkdir(env.MustResolve("Work").Ino, "New"); err != nil {
		t.Fatal(err)
	}
	if _, err := env.FS.FolderAt("Work/New"); !errors.Is(err, core.ErrNoFileID) {
		t.Fatalf("a folder Drive has not taken yet: %v", err)
	}
}

func TestAFolderIsFollowedByItsFileID(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	work := env.Emu.Mkdir(env.Emu.Root(), "Work")
	docs := env.Emu.Mkdir(work, "Docs")
	env.Start()
	was, err := env.FS.Folder(docs)
	if err != nil || was.Path != "Work/Docs" || was.Gone != "" {
		t.Fatalf("the folder is %+v, %v", was, err)
	}
	env.Emu.Update(docs, func(f *emulator.File) { f.Name, f.Parents = "Papers", []string{env.Emu.Root()} })
	poll(t, env)
	now, err := env.FS.Folder(docs)
	if err != nil || now != (core.Folder{Path: "Papers", Ino: was.Ino}) {
		t.Fatalf("after the move the folder is %+v, %v; it was %+v", now, err, was)
	}
}

func TestAFolderGoneFromTheMountSaysWhy(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		gone func(e *emulator.Emulator, work, docs string)
		want string
	}{
		"trashed":        {func(e *emulator.Emulator, _, docs string) { trash(e, docs) }, core.GoneTrash},
		"under a trash":  {func(e *emulator.Emulator, work, _ string) { trash(e, work) }, core.GoneTrash},
		"deleted":        {func(e *emulator.Emulator, _, docs string) { e.Delete(docs) }, core.GoneDeleted},
		"parent deleted": {func(e *emulator.Emulator, work, _ string) { e.Delete(work) }, core.GoneDeleted},
		"left My Drive": {func(e *emulator.Emulator, _, docs string) {
			e.TellParentless(emulator.ParentlessShown)
			e.Update(docs, func(f *emulator.File) { f.Parents = nil })
		}, core.GoneDeleted},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			env := testenv.New(t)
			work := env.Emu.Mkdir(env.Emu.Root(), "Work")
			docs := env.Emu.Mkdir(work, "Docs")
			env.Start()
			c.gone(env.Emu, work, docs)
			poll(t, env)
			if got, err := env.FS.Folder(docs); err != nil || got.Gone != c.want {
				t.Fatalf("the folder is %+v, %v; want gone as %q", got, err, c.want)
			}
		})
	}
}

func trash(e *emulator.Emulator, id string) {
	e.Update(id, func(f *emulator.File) { f.Trashed = true })
}

func TestLostAndFoundIsAFolderEvenWhenNotThere(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	got, err := env.FS.Folder(core.LostFolder)
	if err != nil || got != (core.Folder{Path: meta.LostName}) {
		t.Fatalf("Lost and Found with nothing in it is %+v, %v", got, err)
	}
}
