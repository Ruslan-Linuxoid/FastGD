package core_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/cache"
	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestSwapCutByAKillIsFinishedAtTheStart(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id := env.Emu.WriteFile(env.Emu.Root(), "doc.txt", []byte("old"))
	env.Start()
	writeFile(t, env, "", "doc.tmp", []byte("new content"))
	had := item(t, env, "doc.tmp").ID
	rename(t, env, "", "doc.tmp", "", "doc.txt")

	if err := env.FS.Store().SetState("swapped:"+id, had); err != nil {
		t.Fatal(err)
	}
	if err := env.FS.Close(); err != nil {
		t.Fatal(err)
	}
	env.FS = nil
	content := core.ContentDir(env.Dir)
	if err := os.MkdirAll(filepath.Dir(cache.PathIn(content, had)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(cache.PathIn(content, id), cache.PathIn(content, had)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cache.PathIn(content, id), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := env.Open(); err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, env, "doc.txt"); string(got) != "new content" {
		t.Fatalf("after the start doc.txt reads %q", got)
	}
	uploadAll(t, env)
	requireSaved(t, env, id, "doc.txt", "new content", 2)
}

func TestWriterOfTheRenamedFileGoesOn(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id := env.Emu.WriteFile(env.Emu.Root(), "doc.txt", []byte("old"))
	env.Start()
	_, fh := createFile(t, env, "", "doc.tmp")
	write(t, env, fh, 0, []byte("first half, "))
	rename(t, env, "", "doc.tmp", "", "doc.txt")
	write(t, env, fh, 12, []byte("second half"))
	if got := mustRead(t, env, fh, 0, 23); string(got) != "first half, second half" {
		t.Fatalf("the handle reads %q", got)
	}
	env.FS.Release(fh)
	uploadAll(t, env)
	requireSaved(t, env, id, "doc.txt", "first half, second half", 2)
}

func TestReaderOfTheSavedOverFileKeepsItsVersion(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id := env.Emu.WriteFile(env.Emu.Root(), "doc.txt", []byte("old"))
	env.Start()
	fh := openFile(t, env, "doc.txt")
	if got := mustRead(t, env, fh, 0, 3); string(got) != "old" {
		t.Fatalf("the handle reads %q", got)
	}
	writeFile(t, env, "", "doc.tmp", []byte("new"))
	rename(t, env, "", "doc.tmp", "", "doc.txt")
	if got := mustRead(t, env, fh, 0, 3); string(got) != "old" {
		t.Fatalf("after the save the handle reads %q", got)
	}
	env.FS.Release(fh)
	if got := readAll(t, env, "doc.txt"); string(got) != "new" {
		t.Fatalf("doc.txt reads %q", got)
	}
	uploadAll(t, env)
	requireSaved(t, env, id, "doc.txt", "new", 2)
}
