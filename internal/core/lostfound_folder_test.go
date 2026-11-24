package core_test

import (
	"slices"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestLocalVersionGoesToLostAndFoundWithoutFileIDs(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id, local := report(t, env)
	theirs := testenv.RandomBytes(10000, 405)
	env.Emu.Update(id, func(f *emulator.File) { f.Content = theirs })
	poll(t, env)

	requireShelved(t, env, "Отчёты/2026", "a", ".odt", local)
	env.Restart()
	for {
		if _, err := env.FS.Store().TakeID(); err != nil {
			break
		}
	}

	fh := openWriter(t, env, "Отчёты/2026/a.odt", 0)
	write(t, env, fh, 0, []byte("mine again"))
	env.FS.Release(fh)
	mine := append([]byte(nil), theirs...)
	copy(mine, "mine again")
	fh = openWriter(t, env, "Отчёты/2026/a.odt", 0)
	mustRead(t, env, fh, 0, len(mine))
	env.FS.Release(fh)
	env.Emu.Update(id, func(f *emulator.File) { f.Content = []byte("theirs again") })
	poll(t, env)
	names := lostNames(env, lostDir+"/Отчёты/2026")
	if len(names) != 2 {
		t.Fatalf("Lost and Found holds %q, want the two local versions", names)
	}
	keyed := 0
	for _, n := range names {
		if meta.IsLocalKey(item(t, env, lostDir+"/Отчёты/2026/"+n).ID) {
			keyed++
		}
	}
	if keyed != len(names) {
		t.Fatalf("%d of the %d local versions live under a key of their own, want every one", keyed, len(names))
	}
	if got := readAll(t, env, "Отчёты/2026/a.odt"); string(got) != "theirs again" {
		t.Fatalf("the file reads %q under its own name", got)
	}
}

func TestLostAndFoundKeepsItsNameFromAFolderOfDrive(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	theirs := env.Emu.Mkdir(env.Emu.Root(), meta.LostName)
	env.Emu.WriteFile(theirs, "inside.txt", []byte("of Drive"))
	id, _ := report(t, env)
	check := func(when string) {
		t.Helper()
		if it := item(t, env, meta.LostName); it.ID != theirs {
			t.Fatalf("%s the name in My Drive shows %q, want the folder of Drive", when, it.ID)
		}
		if got := env.Names(meta.LostName); !slices.Equal(got, []string{"inside.txt"}) {
			t.Fatalf("%s the folder of Drive holds %q", when, got)
		}
	}
	check("before any conflict")
	env.Emu.Update(id, func(f *emulator.File) { f.Content = []byte("theirs") })
	poll(t, env)
	check("after the conflict")
	if got := env.Names("/"); !slices.Contains(got, meta.LostName) || !slices.Contains(got, meta.MyDriveName) {
		t.Fatalf("the root of the mount lists %q, want Lost and Found beside My Drive", got)
	}
	env.Restart()
	check("after a restart")
	if !hasLost(env) {
		t.Fatal("after a restart Lost and Found is gone from the root of the mount")
	}
}
