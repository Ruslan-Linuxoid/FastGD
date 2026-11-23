package core_test

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"syscall"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestNoteTellsWhereAConflictCameFrom(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id, local := report(t, env)
	env.Emu.Update(id, func(f *emulator.File) { f.Content = []byte("theirs") })
	poll(t, env)
	as := requireShelved(t, env, "Отчёты/2026", "a", ".odt", local)
	text := requireNote(t, env, lostDir+"/Отчёты/2026", as)
	for _, want := range []string{"Лежал в Drive: Отчёты/2026/a.odt\n",
		"Попал сюда: " + testenv.Epoch().Local().Format("2006-01-02"),
		"Причина: " + "файл изменён здесь, а в Drive за это время появилась другая его версия"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the note says %q, want it to tell %q", text, want)
		}
	}
	uploadAll(t, env)
	for _, f := range env.Emu.All() {
		if isNote(f.Name) {
			t.Fatalf("the note went to Drive as %q", f.Name)
		}
	}
	if n := lostFiles(t, env); n != 1 {
		t.Fatalf("status counts %d files in Lost and Found, want the local version alone", n)
	}
}

func TestNoteTellsWhatDriveAnsweredToAChangeItUndid(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Emu.SetUploadFault(func(name string) int {
		if name == "bad.txt" {
			return http.StatusBadRequest
		}
		return 0
	})
	env.Start()
	bad := testenv.RandomBytes(5000, 430)
	writeFile(t, env, "", "bad.txt", bad)
	rounds(env, 5)
	as := requireShelved(t, env, "", "bad", ".txt", bad)
	text := requireNote(t, env, lostDir, as)
	if !strings.Contains(text, "Лежал в Drive: bad.txt\n") || !strings.Contains(text,
		"Причина: Drive не принял создание файла: ") || !strings.Contains(text, "400") {
		t.Fatalf("the note says %q, want the path and Drive's answer", text)
	}
}

func TestNoteGoesWithItsFile(t *testing.T) {
	t.Parallel()
	for name, gone := range map[string]func(env *testenv.Env, dir, as string){
		"taken out": func(env *testenv.Env, dir, as string) { rename(t, env, dir, as, "", "mine.odt") },
		"deleted": func(env *testenv.Env, dir, as string) {
			if err := env.FS.Unlink(env.MustResolve(dir).Ino, as); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			env := testenv.New(t)
			id, local := report(t, env)
			env.Emu.Update(id, func(f *emulator.File) { f.Content = []byte("theirs") })
			poll(t, env)
			dir := lostDir + "/Отчёты/2026"
			as := requireShelved(t, env, "Отчёты/2026", "a", ".odt", local)
			rec := &recorder{}
			env.FS.SetNotifier(rec)
			in := env.MustResolve(dir).Ino
			gone(env, dir, as)

			requireNoLost(t, env)

			waitUntil(t, func() bool { return heard(rec, in, as+noteSuffix) })
			if name == "taken out" {
				if got := readAll(t, env, "mine.odt"); !bytes.Equal(got, local) {
					t.Fatal("the file taken out of Lost and Found reads different")
				}
			}
		})
	}
}

func TestNoteOfAFileDeletedWhileOpenGoesWhenItCloses(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id, local := report(t, env)
	env.Emu.Update(id, func(f *emulator.File) { f.Content = []byte("theirs") })
	poll(t, env)
	dir := lostDir + "/Отчёты/2026"
	as := requireShelved(t, env, "Отчёты/2026", "a", ".odt", local)
	fh, err := env.FS.Open(context.Background(), env.MustResolve(dir+"/"+as).Ino, syscall.O_RDONLY)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.FS.Unlink(env.MustResolve(dir).Ino, as); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, env, fh, 0, len(local)); !bytes.Equal(got, local) {
		t.Fatal("the open file does not read on after it was deleted")
	}
	env.FS.Release(fh)
	requireNoLost(t, env)
}
