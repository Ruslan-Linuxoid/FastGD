package core_test

import (
	"bytes"
	"context"
	"path"
	"regexp"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

const lostDir = "/Lost and Found (FastGD)/My Drive"

const lostTop = "/Lost and Found (FastGD)"

func hasLost(env *testenv.Env) bool {
	_, err := env.Resolve(lostTop)
	return err == nil
}

func conflictName(stem, ext string) *regexp.Regexp {
	return regexp.MustCompile(`^` + regexp.QuoteMeta(stem) + ` \(конфликт \d{4}-\d{2}-\d{2} ` +
		`\d{2}-\d{2}-\d{2}\)` + regexp.QuoteMeta(ext) + `$`)
}

func report(t *testing.T, env *testenv.Env) (id string, local []byte) {
	t.Helper()
	dir := env.Emu.Mkdir(env.Emu.Mkdir(env.Emu.Root(), "Отчёты"), "2026")
	inDrive := testenv.RandomBytes(64<<10, 400)
	id = env.Emu.WriteFile(dir, "a.odt", inDrive)
	env.Start()
	fh := openWriter(t, env, "Отчёты/2026/a.odt", 0)
	patch := testenv.RandomBytes(4096, 401)
	write(t, env, fh, 8192, patch)
	env.FS.Release(fh)
	local = append([]byte(nil), inDrive...)
	copy(local[8192:], patch)
	fh = openWriter(t, env, "Отчёты/2026/a.odt", 0)
	mustRead(t, env, fh, 0, len(local))
	env.FS.Release(fh)
	env.Emu.ResetRequests()
	return id, local
}

const noteSuffix = " (записка).txt"

func isNote(name string) bool {
	return strings.HasSuffix(name, noteSuffix) || strings.Contains(name, " (записка) (") && strings.HasSuffix(name,
		").txt")
}

func lostNames(env *testenv.Env, dir string) []string {
	var out []string
	for _, n := range env.Names(dir) {
		if !isNote(n) {
			out = append(out, n)
		}
	}
	return out
}

func requireNote(t *testing.T, env *testenv.Env, dir, name string) string {
	t.Helper()
	note := path.Join(dir, name+noteSuffix)
	fh, err := env.FS.Open(context.Background(), env.MustResolve(note).Ino, syscall.O_RDONLY)
	if err != nil {
		t.Fatalf("no note beside %s: %v", name, err)
	}
	text := string(mustRead(t, env, fh, 0, int(env.MustResolve(note).Size)))
	env.FS.Release(fh)
	if !strings.Contains(text, "Файл: "+name+"\n") || !strings.Contains(text, "Лежал в Drive: ") || !strings.Contains(text,
		"Причина: ") {
		t.Fatalf("the note of %s says %q", name, text)
	}
	return text
}

func requireShelved(t *testing.T, env *testenv.Env, dir, stem, ext string, want []byte) string {
	t.Helper()
	names := lostNames(env, lostDir+"/"+dir)
	if len(names) != 1 || !conflictName(stem, ext).MatchString(names[0]) {
		t.Fatalf("%s/%s holds %q, want one conflict of %s%s", lostDir, dir, names, stem, ext)
	}
	requireNote(t, env, path.Join(lostDir, dir), names[0])
	p := lostDir + "/" + dir + "/" + names[0]
	a := env.MustResolve(p)
	fh, err := env.FS.Open(context.Background(), a.Ino, syscall.O_RDONLY)
	if err != nil {
		t.Fatal(err)
	}
	defer env.FS.Release(fh)
	if got := mustRead(t, env, fh, 0, int(a.Size)); !bytes.Equal(got, want) {
		t.Fatalf("%s does not hold the local version", p)
	}
	return names[0]
}

func requireEditHereAndInDrive(t *testing.T, viaPoll bool) {
	t.Helper()
	env := testenv.New(t)
	id, local := report(t, env)
	theirs := testenv.RandomBytes(10000, 402)
	env.Emu.Update(id, func(f *emulator.File) { f.Content = theirs })
	if viaPoll {
		poll(t, env)
	} else {
		env.Clock.Advance(20 * time.Minute)
		env.FS.SyncOnce(context.Background())
	}
	if got := readAll(t, env, "Отчёты/2026/a.odt"); !bytes.Equal(got, theirs) {
		t.Fatal("the file does not read as Drive's version under its own name")
	}
	if it := item(t, env, "Отчёты/2026/a.odt"); it.Dirty || it.ID != id {
		t.Fatalf("the file under its own name is dirty=%v with the fileId %s", it.Dirty, it.ID)
	}
	as := requireShelved(t, env, "Отчёты/2026", "a", ".odt", local)
	if n := writesToDrive(env); n != 0 {
		t.Fatalf("%d requests changed Drive", n)
	}
	if f, _ := env.Emu.Get(id); !bytes.Equal(f.Content, theirs) {
		t.Fatal("Drive's version changed")
	}
	if len(env.Logs("Отчёты/2026/a.odt")) == 0 {
		t.Fatal("the log does not name the path of the file that went to Lost and Found")
	}
	requireLostStaysOutOfDrive(t, env, as, local)
}

func TestEditHereAndInDriveKeepsDrivesAndShelvesTheLocal(t *testing.T) {
	t.Parallel()
	for name, viaPoll := range map[string]bool{"from changes": true, "before the upload": false} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			requireEditHereAndInDrive(t, viaPoll)
		})
	}
}

func TestRenameOrMoveInDriveOfAnEditedFileIsNoConflict(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id, local := report(t, env)
	env.Emu.Update(id, func(f *emulator.File) { f.Name, f.Parents = "b.odt", []string{env.Emu.Root()} })
	poll(t, env)
	uploadAll(t, env)
	if f, _ := env.Emu.Get(id); !bytes.Equal(f.Content, local) || f.Name != "b.odt" {
		t.Fatalf("Drive holds %q with other bytes than the edit", f.Name)
	}
	if _, err := env.Resolve(lostDir); err == nil {
		t.Fatal("Lost and Found is there with nothing in it")
	}
}

func lostFiles(t *testing.T, env *testenv.Env) int {
	t.Helper()
	db, err := meta.OpenReadOnly(env.Dir + "/meta.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st, err := db.ReadStatus(time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	return st.LostFiles
}

func requireNoLost(t *testing.T, env *testenv.Env) {
	t.Helper()
	if _, err := env.Resolve(lostDir); err == nil {
		t.Fatalf("Lost and Found is there, holding %q", env.Names(lostDir))
	}
}

func TestEditHereAndDeletionInDriveKeepsTheDeletionAndShelvesTheLocal(t *testing.T) {
	t.Parallel()
	gone := map[string]func(env *testenv.Env, id string){
		"trashed": func(env *testenv.Env, id string) {
			env.Emu.Update(id, func(f *emulator.File) { f.Trashed = true })
		},
		"deleted": func(env *testenv.Env, id string) { env.Emu.Delete(id) },
	}
	for name, remove := range gone {
		for way, viaPoll := range map[string]bool{"from changes": true, "before the upload": false} {
			t.Run(name+" "+way, func(t *testing.T) {
				t.Parallel()
				env := testenv.New(t)
				id, local := report(t, env)
				remove(env, id)
				if viaPoll {
					poll(t, env)
				} else {
					env.Clock.Advance(20 * time.Minute)
					env.FS.SyncOnce(context.Background())
				}
				if names := env.Names("Отчёты/2026"); len(names) != 0 {
					t.Fatalf("the folder of the deleted file holds %q", names)
				}
				requireShelved(t, env, "Отчёты/2026", "a", ".odt", local)
				uploadAll(t, env)
				if n := writesToDrive(env); n != 0 {
					t.Fatalf("%d requests changed Drive", n)
				}
			})
		}
	}
}

func TestDeletionHereAndEditInDriveKeepsTheFile(t *testing.T) {
	t.Parallel()
	for way, viaPoll := range map[string]bool{"from changes": true, "before the upload": false} {
		t.Run(way, func(t *testing.T) {
			t.Parallel()
			env := testenv.New(t)
			id, _ := report(t, env)
			if err := env.FS.Unlink(env.MustResolve("Отчёты/2026").Ino, "a.odt"); err != nil {
				t.Fatal(err)
			}
			theirs := testenv.RandomBytes(5000, 410)
			env.Emu.Update(id, func(f *emulator.File) { f.Content = theirs })
			if viaPoll {
				poll(t, env)
			}
			uploadAll(t, env)
			if got := readAll(t, env, "Отчёты/2026/a.odt"); !bytes.Equal(got, theirs) {
				t.Fatal("the file is not back with Drive's version")
			}
			if f, _ := env.Emu.Get(id); f.Trashed || !bytes.Equal(f.Content, theirs) {
				t.Fatalf("Drive holds the file trashed=%v", f.Trashed)
			}
			requireNoLost(t, env)
		})
	}
}

func requireLostStaysOutOfDrive(t *testing.T, env *testenv.Env, as string, local []byte) {
	t.Helper()

	env.Restart()
	uploadAll(t, env)
	for _, f := range env.Emu.All() {
		if strings.Contains(f.Name, "конфликт") || f.Name == meta.LostName {
			t.Fatalf("%q went to Drive", f.Name)
		}
	}
	if got := readAll(t, env, lostDir+"/Отчёты/2026/"+as); !bytes.Equal(got, local) {
		t.Fatal("after a restart Lost and Found does not hold the local version")
	}
	if it := item(t, env, lostDir+"/Отчёты/2026/"+as); !it.Lost {
		t.Fatal("the file in Lost and Found is not marked as living there")
	}
}
