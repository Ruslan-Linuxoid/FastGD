package core_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestChangeWithoutItsBaseComesBackInLostAndFound(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	content := testenv.RandomBytes(256<<10, 610)
	env.Emu.WriteFile(env.Emu.Root(), "f.bin", content)
	env.Start()

	changedUnfetched(t, env)
	fh := openWriter(t, env, "f.bin", 0)
	write(t, env, fh, 8192, []byte("edit"))
	env.FS.Release(fh)
	if err := env.FS.Close(); err != nil {
		t.Fatal(err)
	}
	env.FS = nil
	if err := os.WriteFile(filepath.Join(env.Dir, "untrusted"), []byte("for the test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env.Start()
	if got := readAll(t, env, "f.bin"); !bytes.Equal(got, content) {
		t.Fatal("the file does not read as Drive holds it")
	}
	names := lostNames(env, lostDir)
	if len(names) != 1 || !conflictName("f", ".bin").MatchString(names[0]) {
		t.Fatalf("Lost and Found holds %q, want the change", names)
	}
	got := readAll(t, env, lostDir+"/"+names[0])
	if len(got) != len(content) || string(got[8192:8196]) != "edit" {
		t.Fatalf("the change in Lost and Found has %d bytes and reads %q where it was written", len(got), got[8192:8196])
	}
	if text := requireNote(t, env, lostDir, names[0]); !strings.Contains(text, "на её месте нули") {
		t.Fatalf("the note says %q", text)
	}
}

func TestKillAfterTheStateWasMovedStillRestores(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	made, edited, lost := unsent(t, env)

	aside := filepath.Join(env.Dir, "untrusted-state-20260102-030405")
	if err := os.Mkdir(aside, 0o700); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(env.Dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "meta.db") || e.Name() == filepath.Base(core.ContentDir("")) {
			if err := os.Rename(filepath.Join(env.Dir, e.Name()), filepath.Join(aside, e.Name())); err != nil {
				t.Fatal(err)
			}
		}
	}
	env.Start()
	requireRestored(t, env, made, edited, lost)
}

func TestMoveCutShortGoesOnIntoItsCopy(t *testing.T) {
	t.Parallel()
	for name, moved := range map[string]struct{ database, mark bool }{
		"after the content":  {false, false},
		"after the database": {true, false},
		"after the mark":     {true, true},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			requireMoveCutShortGoesOn(t, moved.database, moved.mark)
		})
	}
}

func TestMarkWithNoStateSetsNothingAside(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	earlier := filepath.Join(env.Dir, "untrusted-state-20200101-000000")
	if err := os.MkdirAll(core.ContentDir(earlier), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(earlier, "meta.db"), []byte("an earlier state"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(env.Dir, "untrusted"), []byte("again\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	env.Emu.WriteFile(env.Emu.Root(), "theirs.txt", []byte("theirs"))
	env.Start()
	if copies := stateCopies(t, env); marked(env) || len(copies) != 1 {
		t.Fatalf("after the start: marked %v, copies %q, want the earlier one alone", marked(env), copies)
	}
	if got, err := os.ReadFile(filepath.Join(earlier, "meta.db")); err != nil || string(got) != "an earlier state" {
		t.Fatalf("the earlier copy holds %q: %v", got, err)
	}
	if got := readAll(t, env, "theirs.txt"); string(got) != "theirs" {
		t.Fatalf("the daemon that started from nothing reads %q", got)
	}
}

func TestRestoreIntoLostAndFoundCutAfterAStepLosesNothing(t *testing.T) {
	t.Parallel()
	for i, step := range core.RestoreSteps() {
		t.Run(step, func(t *testing.T) {
			t.Parallel()
			requireRestoreCutAfterStep(t, i, step)
		})
	}
}

func TestMarkNamingAPathOutOfTheCacheMovesNothing(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	if err := env.FS.Close(); err != nil {
		t.Fatal(err)
	}
	env.FS = nil
	out := t.TempDir()
	mark := "for the test\ncopy: ../" + filepath.Base(out) + "/untrusted-state-x"
	if err := os.WriteFile(filepath.Join(env.Dir, "untrusted"), []byte(mark), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Open(); err == nil {
		t.Fatal("a start went on with a mark that names a path out of the cache")
	}
	if entries, _ := os.ReadDir(out); len(entries) != 0 {
		t.Fatalf("the state went out of the cache: %d entries", len(entries))
	}
	if _, err := os.Stat(filepath.Join(env.Dir, "meta.db")); err != nil {
		t.Fatalf("the database left its place: %v", err)
	}
}
