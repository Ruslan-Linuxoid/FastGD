package core_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func appendBehindTheDatabase(t *testing.T, env *testenv.Env, id string, more []byte) {
	t.Helper()

	fd, err := os.OpenFile(contentFile(env, id), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fd.Write(more); err != nil {
		t.Fatal(err)
	}
	fd.Close()
}

func requireNotUploadedAndFailing(t *testing.T, env *testenv.Env) {
	t.Helper()
	for _, f := range env.Emu.All() {
		if f.Name == "f.bin" {
			t.Fatalf("Drive holds %d bytes of a file whose state was not taken up", len(f.Content))
		}
	}
	if it := item(t, env, "f.bin"); !it.Dirty || it.LastError == "" {
		t.Fatalf("the file not taken up is dirty %v with the error %q", it.Dirty, it.LastError)
	}
}

func giveFileIDAndClose(t *testing.T, env *testenv.Env) string {
	t.Helper()
	ids, err := env.Drive.GenerateIDs(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.FS.Close(); err != nil {
		t.Fatal(err)
	}
	env.FS = nil
	return ids[0]
}

func rebindAsAKillLeftIt(t *testing.T, env *testenv.Env, key, id string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(contentFile(env, id)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(contentFile(env, key), contentFile(env, id)); err != nil {
		t.Fatal(err)
	}
	db, err := meta.Open(core.DBPath(env.Dir))
	if err != nil {
		t.Fatal(err)
	}
	it, err := db.ByID(key)
	if err == nil {
		err = db.Visible().Rebind(it, id)
	}
	if cerr := db.Close(); err != nil || cerr != nil {
		t.Fatal(err, cerr)
	}
}

func requireFileIDFinishedAtStart(t *testing.T, env *testenv.Env, key, id string) {
	t.Helper()
	if _, err := os.Stat(contentFile(env, key)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the content still has the name of the key after the start: %v", err)
	}
	if left, err := env.FS.Store().Shelf().Unfinished(); err != nil || len(left) != 0 {
		t.Fatalf("the change of the fileId is left unfinished: %v %v", left, err)
	}
	if got := readAll(t, env, "f.txt"); string(got) != "made without a fileId" {
		t.Fatalf("after the start the file reads %q", got)
	}
	uploadAll(t, env)
	if f := inDrive(t, env, "f.txt"); f.ID != id || string(f.Content) != "made without a fileId" {
		t.Fatalf("Drive holds the file under %q with %q, want the fileId %q", f.ID, f.Content, id)
	}
}
