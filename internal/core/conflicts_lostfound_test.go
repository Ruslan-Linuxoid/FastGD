package core_test

import (
	"bytes"
	"syscall"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestLostAndFoundIsOnlyHere(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id, local := report(t, env)
	env.Emu.Update(id, func(f *emulator.File) { f.Content = []byte("theirs") })
	poll(t, env)
	as := requireShelved(t, env, "Отчёты/2026", "a", ".odt", local)
	in := lostDir + "/Отчёты/2026"

	if _, _, err := env.FS.Create(env.MustResolve(in).Ino, "new.txt", syscall.O_WRONLY); err != syscall.EPERM {
		t.Fatalf("a create in Lost and Found got %v, want EPERM", err)
	}
	if _, err := env.FS.Mkdir(env.MustResolve(lostDir).Ino, "dir"); err != syscall.EPERM {
		t.Fatalf("a mkdir in Lost and Found got %v, want EPERM", err)
	}
	if err := env.FS.Rename(env.MustResolve("Отчёты/2026").Ino, "a.odt", env.MustResolve(in).Ino, "a.odt",
		0); err != syscall.EPERM {
		t.Fatalf("a move into Lost and Found got %v, want EPERM", err)
	}

	for _, to := range []string{"", "Отчёты"} {
		if err := env.FS.Rename(env.MustResolve("/").Ino, meta.LostName, env.MustResolve(to).Ino, "mine",
			0); err != syscall.EPERM {
			t.Fatalf("a rename of Lost and Found into %q got %v, want EPERM", to, err)
		}
	}

	env.Emu.ResetRequests()
	rename(t, env, in, as, "Отчёты", "mine.odt")
	requireNoLost(t, env)
	uploadAll(t, env)
	if f := inDrive(t, env, "Отчёты", "mine.odt"); !bytes.Equal(f.Content, local) || f.ID == id {
		t.Fatal("the file taken out of Lost and Found is not in Drive as a new file")
	}
	if f, _ := env.Emu.Get(id); string(f.Content) != "theirs" {
		t.Fatal("Drive's version of the file changed")
	}
}

func TestFileDeletedInLostAndFoundIsThrownAway(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id, local := report(t, env)
	env.Emu.Update(id, func(f *emulator.File) { f.Content = []byte("theirs") })
	poll(t, env)
	as := requireShelved(t, env, "Отчёты/2026", "a", ".odt", local)
	env.Emu.ResetRequests()
	if err := env.FS.Unlink(env.MustResolve(lostDir+"/Отчёты/2026").Ino, as); err != nil {
		t.Fatal(err)
	}

	requireNoLost(t, env)
	uploadAll(t, env)
	if n := writesToDrive(env); n != 0 {
		t.Fatalf("%d requests changed Drive", n)
	}
	if n := lostFiles(t, env); n != 0 {
		t.Fatalf("status counts %d files in Lost and Found", n)
	}
}
