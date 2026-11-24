package core_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func stateCopies(t *testing.T, env *testenv.Env) []string {
	t.Helper()
	copies, err := filepath.Glob(filepath.Join(env.Dir, "untrusted-state-*"))
	if err != nil {
		t.Fatal(err)
	}
	return copies
}

func marked(env *testenv.Env) bool {
	_, err := os.Lstat(filepath.Join(env.Dir, "untrusted"))
	return err == nil
}

func unsent(t *testing.T, env *testenv.Env) (made, edited, lost []byte) {
	t.Helper()
	id, lost := report(t, env)
	env.Emu.Update(id, func(f *emulator.File) { f.Content = []byte("theirs") })
	poll(t, env)
	requireShelved(t, env, "Отчёты/2026", "a", ".odt", lost)
	env.Emu.WriteFile(env.Emu.Root(), "doc.txt", []byte("as Drive has it"))
	poll(t, env)
	readAll(t, env, "doc.txt")
	env.Emu.SetOffline(true)
	made = testenv.RandomBytes(5000, 600)
	mkdir(t, env, "", "new")
	writeFile(t, env, "new", "made.bin", made)
	edited = []byte("as Drive has it, and more")
	fh := openWriter(t, env, "doc.txt", 0)
	write(t, env, fh, 0, edited)
	env.FS.Release(fh)
	if err := env.FS.Close(); err != nil {
		t.Fatal(err)
	}
	env.FS = nil
	env.Emu.SetOffline(false)
	return made, edited, lost
}

func requireStateSetAside(t *testing.T, env *testenv.Env) string {
	t.Helper()
	if marked(env) {
		t.Fatal("the mark is still there after the start that set the state aside")
	}
	copies := stateCopies(t, env)
	if len(copies) != 1 {
		t.Fatalf("the state was set aside into %q, want one copy", copies)
	}
	if _, err := os.Lstat(filepath.Join(copies[0], "meta.db")); err != nil {
		t.Fatalf("the copy holds no database: %v", err)
	}
	return copies[0]
}

func requireLocalChangesBack(t *testing.T, env *testenv.Env, made, edited, lost []byte) {
	t.Helper()
	if got := readAll(t, env, "new/made.bin"); !bytes.Equal(got, made) {
		t.Fatal("the file made here is not back in the mount as it was")
	}
	if got := readAll(t, env, "doc.txt"); !bytes.Equal(got, edited) {
		t.Fatalf("the file changed here reads %q, want the change", got)
	}
	requireShelved(t, env, "Отчёты/2026", "a", ".odt", lost)
	uploadAll(t, env)
	if f := inDrive(t, env, "new", "made.bin"); !bytes.Equal(f.Content, made) {
		t.Fatal("the file made here did not reach Drive as it was")
	}
	if f := inDrive(t, env, "doc.txt"); !bytes.Equal(f.Content, edited) {
		t.Fatalf("Drive holds the changed file as %q", f.Content)
	}
}

func requireSetAsideNoted(t *testing.T, env *testenv.Env, aside string) {
	t.Helper()
	db, err := meta.OpenReadOnly(filepath.Join(env.Dir, "meta.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st, err := db.ReadStatus(time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if st.Discarded.IsZero() || !strings.Contains(st.DiscardedWhat, aside) {
		t.Fatalf("status tells of the state set aside: at %v, %q, want the copy %s", st.Discarded, st.DiscardedWhat,
			aside)
	}
	if len(env.Logs(aside)) == 0 {
		t.Fatal("the log does not name the copy")
	}
}

func requireRestored(t *testing.T, env *testenv.Env, made, edited, lost []byte) {
	t.Helper()
	aside := requireStateSetAside(t, env)
	requireLocalChangesBack(t, env, made, edited, lost)
	requireSetAsideNoted(t, env, aside)
}

func aheadFound(t *testing.T, env *testenv.Env) {
	t.Helper()
	env.Start()
	if err := env.FS.PollChanges(context.Background()); !errors.Is(err, core.ErrUntrusted) {
		t.Fatalf("the poll of a state ahead of Drive got %v", err)
	}
	select {
	case <-env.FS.Distrusted():
	default:
		t.Fatal("the daemon is not told to stop")
	}
	if !marked(env) {
		t.Fatal("no mark in the cache directory")
	}
	if len(stateCopies(t, env)) != 0 {
		t.Fatal("the run that found the state set it aside itself")
	}
}

func TestStateOfANewerBuildIsSetAside(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	unsent(t, env)
	if err := execSQL(filepath.Join(env.Dir, "meta.db"), `PRAGMA user_version = 9999`); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Open(); !errors.Is(err, core.ErrUntrusted) || !marked(env) {
		t.Fatalf("the start on a database of a newer build got %v, marked %v", err, marked(env))
	}
	if len(stateCopies(t, env)) != 0 {
		t.Fatal("the start that found the state set it aside itself")
	}
	env.Start()

	if marked(env) || len(stateCopies(t, env)) != 1 {
		t.Fatalf("after the next start: marked %v, copies %q", marked(env), stateCopies(t, env))
	}
	if names := env.Names(""); len(names) != 2 {
		t.Fatalf("the mount of the new state holds %q, want what Drive holds", names)
	}
	if len(env.Logs("cannot be read: nothing is restored")) == 0 {
		t.Fatal("the log does not tell that nothing was restored")
	}
}

func TestStateAheadOfDriveIsSetAsideAndRestored(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	made, edited, lost := unsent(t, env)
	if err := execSQL(filepath.Join(env.Dir, "meta.db"),
		`UPDATE state SET value = '999999' WHERE key = 'changes_token'`); err != nil {
		t.Fatal(err)
	}
	aheadFound(t, env)
	env.Restart()
	requireRestored(t, env, made, edited, lost)

	env.Restart()
	if len(stateCopies(t, env)) != 1 || marked(env) {
		t.Fatalf("after one more start: copies %q, marked %v", stateCopies(t, env), marked(env))
	}
}

func TestFileIDOfALocalFileInDriveSetsTheStateAside(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	env.Emu.SetOffline(true)
	mine := testenv.RandomBytes(3000, 601)
	writeFile(t, env, "", "mine.bin", mine)

	id := giveID(t, env, "mine.bin")
	env.Emu.SetOffline(false)
	env.Emu.Put(emulator.File{ID: id, Name: "mine.bin", MimeType: "application/octet-stream",
		Parents: []string{env.Emu.Root()}, Content: []byte("another")})
	if err := env.FS.PollChanges(context.Background()); err == nil {
		t.Fatal("the poll that brought the fileId of a local file went through")
	}
	select {
	case <-env.FS.Distrusted():
	default:
		t.Fatal("the daemon is not told to stop")
	}
	if !marked(env) {
		t.Fatal("no mark in the cache directory")
	}
	if it := item(t, env, "mine.bin"); it.RExists {
		t.Fatal("the local file took what Drive holds under its fileId")
	}
	env.Restart()
	if marked(env) || len(stateCopies(t, env)) != 1 {
		t.Fatalf("after the next start: marked %v, copies %q", marked(env), stateCopies(t, env))
	}
	if got := readAll(t, env, "mine.bin"); string(got) != "another" {
		t.Fatalf("under the name the mount shows %q, want what Drive holds", got)
	}
	as := requireShelved(t, env, "", "mine", ".bin", mine)
	if text := requireNote(t, env, lostDir, as); !strings.Contains(text, "Лежал в Drive: My Drive/mine.bin\n") {
		t.Fatalf("the note says %q", text)
	}
}
