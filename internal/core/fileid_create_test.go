package core_test

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestCreateAnsweredThatTheItemExistsIsDone(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	content := testenv.RandomBytes(5000, 540)
	writeFile(t, env, "", "f.bin", content)
	mkdir(t, env, "", "dir")
	env.Emu.LoseUploadAnswers(1000)
	env.Clock.Advance(20 * time.Minute)
	env.FS.SyncOnce(context.Background())
	env.Emu.LoseUploadAnswers(0)
	if it := item(t, env, "f.bin"); !it.Dirty || it.RExists {
		t.Fatalf("scenario not built: with the answer lost the file is dirty %v, known in Drive %v", it.Dirty, it.RExists)
	}
	env.Emu.ResetRequests()
	uploadAll(t, env)
	requireNoContentSent(t, env)
	for _, name := range []string{"f.bin", "dir"} {
		requireDoneAndKnownInDrive(t, env, name)
	}
	if f := inDrive(t, env, "f.bin"); !bytes.Equal(f.Content, content) {
		t.Fatal("Drive holds other content than was written")
	}
	if n := len(env.Emu.All()); n != 2 {
		t.Fatalf("Drive holds %d items, want the file and the folder", n)
	}
	requireNoLost(t, env)
}

func TestCreateAnsweredWithAnItemOfAnotherKindIsAConflict(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	content := testenv.RandomBytes(3000, 541)
	writeFile(t, env, "", "note.txt", content)
	id := giveID(t, env, "note.txt")
	env.Emu.Put(emulator.File{ID: id, Name: "note.txt", MimeType: drive.MimeFolder, Parents: []string{env.Emu.Root()}})
	uploadAll(t, env)
	requireShelved(t, env, "", "note", ".txt", content)
	a, err := env.Resolve("note.txt")
	if err != nil || a.Mode&syscall.S_IFMT != syscall.S_IFDIR {
		t.Fatalf("under the name the mount shows mode %o: %v, want the folder Drive holds", a.Mode, err)
	}
	if f, _ := env.Emu.Get(id); f.MimeType != drive.MimeFolder {
		t.Fatal("the item of Drive was changed")
	}
}

func TestFolderCreateAnsweredWithAFileIsNotDone(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	mkdir(t, env, "", "dir")
	id := giveID(t, env, "dir")
	env.Emu.Put(emulator.File{ID: id, Name: "dir", MimeType: "text/plain", Parents: []string{env.Emu.Root()}})
	env.Clock.Advance(20 * time.Minute)
	env.FS.SyncOnce(context.Background())
	if it := item(t, env, "dir"); it.RExists || !strings.Contains(it.LastError, "text/plain") {
		t.Fatalf("the folder is known in Drive %v with the error %q", it.RExists, it.LastError)
	}
}

func requireNoContentSent(t *testing.T, env *testenv.Env) {
	t.Helper()

	sent := 0
	for _, r := range env.Emu.Requests() {
		if r.Method == http.MethodPatch || r.Method == http.MethodPut {
			sent++
		}
	}
	if sent != 0 {
		t.Fatalf("after the answer that the item exists %d requests changed it", sent)
	}
}

func requireDoneAndKnownInDrive(t *testing.T, env *testenv.Env, name string) {
	t.Helper()
	it := item(t, env, name)
	if it.Dirty || !it.RExists || it.MetaDirty || it.LastError != "" {
		t.Fatalf("%s is dirty %v, known in Drive %v, with changed metadata %v and the error %q", name, it.Dirty, it.RExists,
			it.MetaDirty, it.LastError)
	}
	if f := inDrive(t, env, name); f.ID != it.ID {
		t.Fatalf("Drive holds %s under %q, here it is %q", name, f.ID, it.ID)
	}
}
