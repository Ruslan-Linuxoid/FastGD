package core_test

import (
	"os"
	"syscall"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestCacheDirectoryOfWiderRightsBecomesTheDaemonsAlone(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	if err := os.Chmod(env.Dir, 0o755); err != nil {
		t.Fatal(err)
	}
	env.Start()
	fi, err := os.Stat(env.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if mode := fi.Mode().Perm(); mode != 0o700 {
		t.Fatalf("after the start the cache directory has the rights %o, want 0700", mode)
	}
}

func TestKernelHearsOfTheNoteOfAShelvedFile(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id, _ := report(t, env)
	rec := &recorder{}
	env.FS.SetNotifier(rec)
	env.Emu.Update(id, func(f *emulator.File) { f.Content = []byte("theirs") })
	poll(t, env)
	dir := lostDir + "/Отчёты/2026"
	note := ""
	for _, name := range env.Names(dir) {
		if isNote(name) {
			note = name
		}
	}
	if note == "" {
		t.Fatalf("scenario not built: %s holds %q and no note", dir, env.Names(dir))
	}
	parent := env.MustResolve(dir).Ino
	waitUntil(t, func() bool { return heard(rec, parent, note) })
}

func TestMoveIntoAFileIsNotDir(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	root := env.Emu.Root()
	env.Emu.WriteFile(root, "a.txt", []byte("a"))
	env.Emu.WriteFile(root, "b.txt", []byte("b"))
	env.Start()
	top := env.MustResolve("").Ino
	err := env.FS.Rename(top, "a.txt", env.MustResolve("b.txt").Ino, "a.txt", 0)
	testenv.RequireErrno(t, err, syscall.ENOTDIR)
	if _, err := env.Resolve("a.txt"); err != nil {
		t.Fatalf("after the refused move a.txt is gone: %v", err)
	}
}
