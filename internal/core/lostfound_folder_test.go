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

func requireLostAndFoundRoot(t *testing.T, env *testenv.Env, when string, theirs string, local []byte,
	want []string) {
	t.Helper()
	numbered := lostDir + " (1)"
	if got := env.Names(""); !slices.Equal(got, want) {
		t.Fatalf("%s the root lists %q, want %q", when, got, want)
	}
	if it := item(t, env, lostDir); !it.Lost {
		t.Fatalf("%s the name of Lost and Found shows the item %q of Drive", when, it.ID)
	}
	if it := item(t, env, numbered); it.ID != theirs {
		t.Fatalf("%s %q shows %q, want the folder of Drive", when, numbered, it.ID)
	}
	if got := readAll(t, env, numbered+"/inside.txt"); string(got) != "of Drive" {
		t.Fatalf("%s the file in the folder of Drive reads %q", when, got)
	}
	requireShelved(t, env, "Отчёты/2026", "a", ".odt", local)
}

func TestLostAndFoundKeepsItsNameFromAFolderOfDrive(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	theirs := env.Emu.Mkdir(env.Emu.Root(), lostDir)
	env.Emu.WriteFile(theirs, "inside.txt", []byte("of Drive"))
	id, local := report(t, env)
	if it := item(t, env, lostDir); it.ID != theirs {
		t.Fatalf("scenario not built: before any conflict the name shows %q, want the folder of Drive", it.ID)
	}
	rec := &recorder{}
	env.FS.SetNotifier(rec)
	root := env.MustResolve("").Ino
	env.Emu.Update(id, func(f *emulator.File) { f.Content = []byte("theirs") })
	poll(t, env)
	numbered := lostDir + " (1)"
	check := func(when string, want ...string) {
		t.Helper()
		requireLostAndFoundRoot(t, env, when, theirs, local, want)
	}
	check("after the conflict", lostDir, numbered, "Отчёты")

	waitUntil(t, func() bool { return heard(rec, root, lostDir) })
	env.Restart()
	check("after a restart", lostDir, numbered, "Отчёты")

	later := env.Emu.Mkdir(env.Emu.Root(), lostDir)
	poll(t, env)
	check("with one more folder of the name in Drive", lostDir, numbered, lostDir+" (2)", "Отчёты")
	if it := item(t, env, lostDir+" (2)"); it.ID != later {
		t.Fatalf("the later folder of Drive shows as %q", it.ID)
	}

	rec = &recorder{}
	env.FS.SetNotifier(rec)
	dir := lostDir + "/Отчёты/2026"
	rename(t, env, dir, lostNames(env, dir)[0], "", "mine.odt")
	if got := env.Names(""); !slices.Equal(got, []string{lostDir, numbered, "mine.odt", "Отчёты"}) {
		t.Fatalf("with Lost and Found gone the root lists %q", got)
	}
	if it := item(t, env, lostDir); it.ID != theirs {
		t.Fatalf("with Lost and Found gone its name shows %q, want the earlier folder of Drive", it.ID)
	}
	if it := item(t, env, numbered); it.ID != later {
		t.Fatalf("with Lost and Found gone %q shows %q, want the later folder of Drive", numbered, it.ID)
	}
	waitUntil(t, func() bool { return heard(rec, root, lostDir, numbered, lostDir+" (2)") })
}
