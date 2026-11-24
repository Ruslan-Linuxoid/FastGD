package core_test

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestCreationDriveNeverTakesGoesToLostAndFound(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Emu.SetUploadFault(func(name string) int {
		if name == "bad.txt" {
			return http.StatusBadRequest
		}
		return 0
	})
	env.Start()
	bad := testenv.RandomBytes(5000, 500)
	writeFile(t, env, "", "bad.txt", bad)
	writeFile(t, env, "", "good.txt", []byte("good"))

	rounds(env, 1)
	if f := inDrive(t, env, "good.txt"); string(f.Content) != "good" {
		t.Fatal("the file beside the failing one is not in Drive")
	}
	if names := env.Names(""); len(names) != 2 {
		t.Fatalf("after one try the root holds %q", names)
	}
	rounds(env, 4)
	if names := env.Names(""); len(names) != 1 || names[0] != "good.txt" || !hasLost(env) {
		t.Fatalf("after five tries the root holds %q", names)
	}
	requireShelved(t, env, "", "bad", ".txt", bad)
	if len(env.Logs("bad.txt")) == 0 {
		t.Fatal("the log does not tell of the file Drive did not take")
	}
	env.Emu.ResetRequests()
	rounds(env, 3)
	if n := len(env.Emu.Requests()); n != 0 {
		t.Fatalf("%d more requests for what was undone", n)
	}
}

func TestContentDriveNeverTakesIsUndone(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	theirs := testenv.RandomBytes(8192, 510)
	id := env.Emu.WriteFile(env.Emu.Root(), "f.bin", theirs)
	env.Start()
	fh := openWriter(t, env, "f.bin", 0)
	mustRead(t, env, fh, 0, len(theirs))
	patch := testenv.RandomBytes(4096, 511)
	write(t, env, fh, 0, patch)
	env.FS.Release(fh)
	mine := append([]byte(nil), theirs...)
	copy(mine, patch)
	env.Emu.SetUploadFault(func(string) int { return http.StatusForbidden })
	rounds(env, 5)
	if got := readAll(t, env, "f.bin"); !bytes.Equal(got, theirs) {
		t.Fatal("the file does not read as Drive's version after the edit was undone")
	}
	requireShelved(t, env, "", "f", ".bin", mine)
	if f, _ := env.Emu.Get(id); !bytes.Equal(f.Content, theirs) {
		t.Fatal("Drive's version changed")
	}
}

func TestRenameDriveNeverTakesIsUndone(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id := env.Emu.WriteFile(env.Emu.Root(), "a.txt", []byte("a"))
	env.Start()

	env.Emu.Update(id, func(f *emulator.File) { f.CannotRename = true })
	rename(t, env, "", "a.txt", "", "b.txt")
	rounds(env, 5)
	if names := env.Names(""); len(names) != 1 || names[0] != "a.txt" {
		t.Fatalf("the root holds %q, want the name Drive kept", names)
	}
	if it := item(t, env, "a.txt"); it.MetaDirty || it.Attempts != 0 {
		t.Fatalf("the item is left changed=%v with %d failed tries", it.MetaDirty, it.Attempts)
	}
	if len(env.Logs("b.txt")) == 0 {
		t.Fatal("the log does not tell of the rename that was undone")
	}
	requireNoLost(t, env)
}

func TestFolderDriveNeverTakesShelvesWhatLiesInIt(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	moved := env.Emu.WriteFile(env.Emu.Root(), "moved.txt", []byte("m"))
	env.Emu.SetUploadFault(func(name string) int {
		if name == "bad" {
			return http.StatusBadRequest
		}
		return 0
	})
	env.Start()
	mkdir(t, env, "", "bad")
	mkdir(t, env, "bad", "sub")
	note := testenv.RandomBytes(300, 520)
	writeFile(t, env, "bad/sub", "note.txt", note)

	writeFile(t, env, "bad/sub", ".note.txt.swp", []byte("swap"))
	rename(t, env, "", "moved.txt", "bad", "moved.txt")
	rounds(env, 6)
	if names := env.Names(""); len(names) != 1 || names[0] != "moved.txt" || !hasLost(env) {
		t.Fatalf("the root holds %q, want Lost and Found and the file moved back", names)
	}
	requireShelved(t, env, "bad/sub", "note", ".txt", note)
	if f, _ := env.Emu.Get(moved); f.Parents[0] != env.Emu.Root() {
		t.Fatal("the file moved into the folder Drive did not take left its place in Drive")
	}
	for _, f := range env.Emu.All() {
		if f.Name == "bad" || f.Name == "sub" || strings.HasPrefix(f.Name, "note") {
			t.Fatalf("%q went to Drive", f.Name)
		}
	}
}

func TestOperationCutByAKillGoesAgainOnce(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	content := testenv.RandomBytes(3000, 530)
	writeFile(t, env, "", "f.bin", content)
	if err := env.FS.Close(); err != nil {
		t.Fatal(err)
	}
	env.FS = nil

	if err := execSQL(env.Dir+"/meta.db", `UPDATE ops SET state = 1`); err != nil {
		t.Fatal(err)
	}
	env.Start()
	uploadAll(t, env)
	hits := 0
	for _, f := range env.Emu.All() {
		if f.Name == "f.bin" {
			hits++
		}
	}
	if f := inDrive(t, env, "f.bin"); hits != 1 || !bytes.Equal(f.Content, content) {
		t.Fatalf("Drive holds %d files of the name", hits)
	}
}

func TestOnlyRefusalsInARowUndoAChange(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	fault := http.StatusBadRequest
	env.Emu.SetUploadFault(func(name string) int {
		if strings.HasPrefix(name, "bad") {
			return fault
		}
		return 0
	})
	env.Start()
	content := testenv.RandomBytes(2000, 540)
	writeFile(t, env, "", "bad.txt", content)
	rounds(env, 4)
	fault = http.StatusServiceUnavailable
	rounds(env, 1)
	fault = http.StatusBadRequest
	rounds(env, 4)
	if names := env.Names(""); len(names) != 1 || names[0] != "bad.txt" {
		t.Fatalf("four refusals, a failure of the server and four refusals left %q", names)
	}

	rename(t, env, "", "bad.txt", "", "bad too.txt")
	rounds(env, 4)
	if names := env.Names(""); len(names) != 1 || names[0] != "bad too.txt" {
		t.Fatalf("four refusals of the renamed file left %q", names)
	}
	rounds(env, 1)
	requireShelved(t, env, "", "bad too", ".txt", content)
}

func TestTrashingDriveNeverTakesKeepsTheEdit(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id := env.Emu.WriteFile(env.Emu.Root(), "f.txt", []byte("theirs"))
	env.Start()
	fh := openWriter(t, env, "f.txt", 0)
	write(t, env, fh, 0, []byte("mine!!"))
	env.FS.Release(fh)
	env.Emu.Update(id, func(f *emulator.File) { f.CannotTrash = true })

	if err := env.FS.Unlink(env.MustResolve("").Ino, "f.txt"); err != nil {
		t.Fatal(err)
	}
	rounds(env, 7)
	if f, _ := env.Emu.Get(id); f.Trashed || string(f.Content) != "mine!!" {
		t.Fatalf("Drive holds the file trashed=%v with %q", f.Trashed, f.Content)
	}
	if names := env.Names(""); len(names) != 1 || names[0] != "f.txt" {
		t.Fatalf("the root holds %q", names)
	}
}

func TestFailingTryShowsWhileTheCallGoesAgain(t *testing.T) {
	testenv.InBubble(t, func(t *testing.T, env *testenv.Env) {
		env.Start()
		writeFile(t, env, "", "f.txt", []byte("content"))
		env.Emu.SetOffline(true)
		done := make(chan struct{})
		go func() {
			env.FS.SyncOnce(drive.InBackground(context.Background()))
			close(done)
		}()

		time.Sleep(50 * time.Millisecond)
		if it := item(t, env, "f.txt"); it.Attempts != 1 || it.LastError == "" || !it.NextTry.After(time.Now()) {
			t.Fatalf("a moment into a failing upload the item has %d attempts, the error %q, the next try at %v", it.Attempts,
				it.LastError, it.NextTry)
		}
		env.Emu.SetOffline(false)
		<-done
		if it := item(t, env, "f.txt"); it.Dirty || it.Attempts != 0 || it.LastError != "" {
			t.Fatalf("after the upload went through: dirty %v, %d attempts, error %q", it.Dirty, it.Attempts, it.LastError)
		}
	})
}
