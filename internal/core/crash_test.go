package core_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestLostAnswerToAnUpdateOfAnEditedFileIsSettled(t *testing.T) {
	env, content := fromDrive(t, 3<<20, 700)
	fh := openWriter(t, env, "f.bin", 0)
	write(t, env, fh, 1<<20, []byte("edit"))
	env.FS.Release(fh)
	copy(content[1<<20:], "edit")
	env.Emu.LoseUploadAnswers(1000)
	env.Clock.Advance(20 * time.Minute)
	env.FS.SyncOnce(context.Background())
	if f := inDrive(t, env, "f.bin"); !bytes.Equal(f.Content, content) {
		t.Fatal("scenario not built: Drive did not take the content")
	}
	if it := item(t, env, "f.bin"); !it.Dirty {
		t.Fatal("scenario not built: the file is clean with its answer lost")
	}
	env.Emu.LoseUploadAnswers(0)
	env.Emu.ResetRequests()
	uploadAll(t, env)
	it := item(t, env, "f.bin")
	if it.Dirty || it.LastError != "" || it.Base != 0 || it.MD5 != md5Of(content) {
		t.Fatalf("the file is dirty %v, base %d, error %q", it.Dirty, it.Base, it.LastError)
	}
	if u := uploadsSeen(t, env); u.simple != 0 {
		t.Fatalf("the content Drive had went up %d times more", u.simple)
	}
	requireNoLost(t, env)
}

func deleteDatabase(t *testing.T, env *testenv.Env) {
	t.Helper()
	if err := env.FS.Close(); err != nil {
		t.Fatal(err)
	}
	env.FS = nil
	dbs, _ := filepath.Glob(filepath.Join(env.Dir, "meta.db*"))
	for _, p := range dbs {
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
}

func TestContentTheDatabaseDoesNotKnowIsCleanCache(t *testing.T) {
	testenv.InBubble(t, func(t *testing.T, env *testenv.Env) {
		env.CacheSize = mib
		files(env, 2, 600*kib)
		env.Start()
		content := testenv.RandomBytes(300*kib, 710)
		writeFile(t, env, "", "only here.bin", content)
		id := item(t, env, "only here.bin").ID
		deleteDatabase(t, env)
		env.Start()
		lies := contentFile(env, id)
		if kept, err := os.ReadFile(lies); err != nil || !bytes.Equal(kept, content) {
			t.Fatalf("the content of the file the database lost does not lie where it did: %v", err)
		}
		if _, err := os.Lstat(filepath.Join(core.ContentDir(env.Dir), ".orphans")); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("the cache has a folder for content kept aside: %v", err)
		}
		if used := env.FS.CacheUsed(); used < 300*kib || used > 320*kib {
			t.Fatalf("the cache counts %d bytes, the content the database does not know takes 300 KiB", used)
		}

		readClosed(t, env, "f0")
		readClosed(t, env, "f1")
		time.Sleep(time.Minute)
		if _, err := os.Lstat(lies); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("with the cache over its limit the content the database does not know stays: %v", err)
		}
		if used := env.FS.CacheUsed(); used > mib {
			t.Fatalf("the cache counts %d bytes, over its limit", used)
		}
	})
}

func TestContentAnOlderBuildKeptAsideIsCleanCache(t *testing.T) {
	testenv.InBubble(t, func(t *testing.T, env *testenv.Env) {
		env.CacheSize = mib
		files(env, 2, 600*kib)
		aside := filepath.Join(core.ContentDir(env.Dir), ".orphans")
		if err := os.MkdirAll(aside, 0o700); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"some-file-id", "some-file-id.1"} {
			if err := os.WriteFile(filepath.Join(aside, name), testenv.RandomBytes(100*kib, 711), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		env.Start()
		if used := env.FS.CacheUsed(); used < 200*kib || used > 220*kib {
			t.Fatalf("the cache counts %d bytes, the content kept aside takes 200 KiB", used)
		}
		readClosed(t, env, "f0")
		readClosed(t, env, "f1")
		time.Sleep(time.Minute)
		if _, err := os.Lstat(aside); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("with the cache over its limit the folder of content kept aside stays: %v", err)
		}
	})
}

func TestItemTakesThePathOfContentNoItemHad(t *testing.T) {
	testenv.InBubble(t, func(t *testing.T, env *testenv.Env) {
		env.CacheSize = mib
		const id = "abStrayFileID"
		lies := contentFile(env, id)
		if err := os.MkdirAll(filepath.Dir(lies), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(lies, testenv.RandomBytes(200*kib, 730), 0o600); err != nil {
			t.Fatal(err)
		}
		env.Start()
		if used := env.FS.CacheUsed(); used < 200*kib {
			t.Fatalf("scenario not built: the cache counts %d bytes", used)
		}
		content := testenv.RandomBytes(100*kib, 731)
		env.Emu.Put(emulator.File{ID: id, Name: "came.bin", MimeType: "application/octet-stream",
			Parents: []string{env.Emu.Root()}, Content: content})
		poll(t, env)
		if got := readClosed(t, env, "came.bin"); !bytes.Equal(got, content) {
			t.Fatal("the file reads other content than Drive holds")
		}
		if used := env.FS.CacheUsed(); used < 100*kib || used > 120*kib {
			t.Fatalf("the cache counts %d bytes, the file takes 100 KiB", used)
		}

		time.Sleep(time.Minute)
		env.Emu.SetOffline(true)
		if got := readClosed(t, env, "came.bin"); !bytes.Equal(got, content) {
			t.Fatal("the content of the file is gone from the cache")
		}
	})
}

func TestDatabaseFromAnOlderCopyGivesNoFileIDTheCacheHolds(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	if err := env.FS.Close(); err != nil {
		t.Fatal(err)
	}
	env.FS = nil
	dbs, _ := filepath.Glob(filepath.Join(env.Dir, "meta.db*"))
	older := map[string][]byte{}
	for _, p := range dbs {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		older[p] = b
	}
	env.Start()

	env.Emu.SetOffline(true)
	taken := map[string]bool{}
	for i := range 5 {
		name := fmt.Sprintf("since %d.bin", i)
		writeFile(t, env, "", name, testenv.RandomBytes(3000, uint64(720+i)))
		taken[item(t, env, name).ID] = true
	}
	deleteDatabase(t, env)
	for p, b := range older {
		if err := os.WriteFile(p, b, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	env.Start()

	for i := range 10 {
		name := fmt.Sprintf("new %d.bin", i)
		content := []byte(name)
		writeFile(t, env, "", name, content)
		if id := item(t, env, name).ID; taken[id] {
			t.Fatalf("the new file %q got the fileId %s, under which the cache holds other content", name, id)
		}
		if got := readAll(t, env, name); !bytes.Equal(got, content) {
			t.Fatalf("the new file %q reads other content than was written", name)
		}
	}
}

func TestOneBrokenFileDoesNotStopAStart(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	writeFile(t, env, "", "broken.bin", []byte("cannot be read at the start"))
	writeFile(t, env, "", "fine.bin", []byte("fine"))
	id := item(t, env, "broken.bin").ID
	if err := env.FS.Close(); err != nil {
		t.Fatal(err)
	}
	env.FS = nil
	replaceContentWithLoop(t, env, id)
	if _, err := env.Open(); err != nil {
		t.Fatalf("the start fails for one file: %v", err)
	}
	if got := readAll(t, env, "fine.bin"); string(got) != "fine" {
		t.Fatalf("the other file reads %q", got)
	}
	if len(env.Logs("broken.bin")) == 0 {
		t.Fatal("the log does not name the file that could not be recovered")
	}

	rounds(env, 2)
	if it := item(t, env, "broken.bin"); !it.Dirty || it.LastError == "" {
		t.Fatalf("the file that could not be recovered is dirty %v with error %q", it.Dirty, it.LastError)
	}
	for _, f := range env.Emu.All() {
		if f.Name == "broken.bin" {
			t.Fatalf("Drive holds %d bytes under the name of the file that could not be read", len(f.Content))
		}
	}
}

func replaceContentWithLoop(t *testing.T, env *testenv.Env, id string) {
	t.Helper()

	p := contentFile(env, id)
	if err := os.Remove(p); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(p, p); err != nil {
		t.Fatal(err)
	}
}
