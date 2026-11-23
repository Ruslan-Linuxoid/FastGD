package core_test

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestTreeIsReadAnewWhenTheFieldsChange(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	old := env.Emu.WriteFile(env.Emu.Root(), "old.txt", []byte("old"))
	gone := env.Emu.WriteFile(env.Emu.Root(), "gone.txt", []byte("gone"))
	env.Emu.WriteFile(env.Emu.Root(), "edited.txt", []byte("as Drive has it"))
	env.Start()
	rev := item(t, env, "old.txt").Rev
	if rev == "" {
		t.Fatal("scenario not built: the item has no revision")
	}
	fh := openWriter(t, env, "edited.txt", 0)
	write(t, env, fh, 0, []byte("as it is changed here"))
	env.FS.Release(fh)
	env.Emu.SetOffline(true)
	writeFile(t, env, "", "made.txt", []byte("made here"))
	if err := env.FS.Close(); err != nil {
		t.Fatal(err)
	}
	env.FS = nil
	env.Emu.SetOffline(false)

	if err := execSQL(filepath.Join(env.Dir, "meta.db"), `UPDATE items SET rev = ''`); err != nil {
		t.Fatal(err)
	}
	env.Emu.Delete(gone)

	env.Tune = func(o *core.Options) { o.FieldsMark = "another set" }
	env.Start()
	requireTreeReadAnew(t, env, old, rev)
	requireNextStartAsksNothing(t, env)
}

func TestTreeReadWithOtherFieldsWaitsForTheNetwork(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Emu.WriteFile(env.Emu.Root(), "old.txt", []byte("old"))
	env.Start()
	rev := item(t, env, "old.txt").Rev
	if err := env.FS.Close(); err != nil {
		t.Fatal(err)
	}
	env.FS = nil
	if err := execSQL(filepath.Join(env.Dir, "meta.db"), `UPDATE items SET rev = ''`); err != nil {
		t.Fatal(err)
	}
	env.Tune = func(o *core.Options) { o.FieldsMark = "another set" }
	env.Emu.SetOffline(true)
	env.Start()
	if it := item(t, env, "old.txt"); it.Rev != "" {
		t.Fatalf("scenario not built: without the network the item has the revision %q", it.Rev)
	}
	env.Emu.SetOffline(false)
	env.Restart()
	if it := item(t, env, "old.txt"); it.Rev != rev {
		t.Fatalf("after the start with the network the item has the revision %q, want %q", it.Rev, rev)
	}
}

func requireTreeReadAnew(t *testing.T, env *testenv.Env, old, rev string) {
	t.Helper()
	if it := item(t, env, "old.txt"); it.Rev != rev || it.ID != old {
		t.Fatalf("after the reading anew the item has the revision %q, want %q", it.Rev, rev)
	}
	if _, err := env.Resolve("gone.txt"); err == nil {
		t.Fatal("the file deleted in Drive is still in the mount")
	}
	if it := item(t, env, "edited.txt"); !it.Dirty {
		t.Fatal("the file changed here is no longer changed")
	}
	if got := readAll(t, env, "edited.txt"); !bytes.Equal(got, []byte("as it is changed here")) {
		t.Fatalf("the file changed here reads %q", got)
	}
	if got := readAll(t, env, "made.txt"); string(got) != "made here" {
		t.Fatalf("the file made here reads %q", got)
	}
	uploadAll(t, env)
	if f := inDrive(t, env, "edited.txt"); string(f.Content) != "as it is changed here" {
		t.Fatalf("Drive holds the changed file as %q", f.Content)
	}
	if f := inDrive(t, env, "made.txt"); string(f.Content) != "made here" {
		t.Fatalf("Drive holds the file made here as %q", f.Content)
	}
}

func requireNextStartAsksNothing(t *testing.T, env *testenv.Env) {
	t.Helper()

	if err := env.FS.Close(); err != nil {
		t.Fatal(err)
	}
	env.FS = nil
	env.Emu.ResetRequests()
	env.Start()
	if n := len(env.Emu.Requests()); n != 0 {
		t.Fatalf("a start with the same fields made %d requests", n)
	}
}
