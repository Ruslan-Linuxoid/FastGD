package core_test

import (
	"bytes"
	"errors"
	"net/http"
	"sync"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

var errDiskRead = errors.New("the disk failed the read")

func failReads(env *testenv.Env, ino meta.Ino, skip, n int) (arm func()) {
	var mu sync.Mutex
	armed := false
	env.FS.Store().FailItemReads(func(it meta.Item) error {
		mu.Lock()
		defer mu.Unlock()
		if !armed || it.Ino != ino {
			return nil
		}
		if skip > 0 {
			skip--
			return nil
		}
		if n == 0 {
			return nil
		}
		n--
		return errDiskRead
	})
	return func() {
		mu.Lock()
		armed = true
		mu.Unlock()
	}
}

func TestPollThatCannotReadAnItemGoesAgain(t *testing.T) {
	t.Parallel()
	env, content := fromDrive(t, 64<<10, 1520)
	if got := readAll(t, env, "f.bin"); !bytes.Equal(got, content) {
		t.Fatal("scenario not built: the file does not read")
	}
	id, ino := item(t, env, "f.bin").ID, item(t, env, "f.bin").Ino

	if err := env.FS.CheckCache(t.Context(), item(t, env, "f.bin").Item); err != nil {
		t.Fatal(err)
	}
	theirs := testenv.RandomBytes(64<<10, 1521)
	env.Emu.Update(id, func(f *emulator.File) { f.Content = theirs })

	failReads(env, ino, 1, 1)()
	token, _, _ := env.FS.Store().GetState(meta.StateChangesToken)
	if err := env.FS.PollChanges(t.Context()); err == nil {
		t.Fatal("a poll that could not read the item went through")
	}
	if now, _, _ := env.FS.Store().GetState(meta.StateChangesToken); now != token {
		t.Fatal("a poll that failed moved the token on")
	}
	if it := item(t, env, "f.bin"); it.MD5 == md5Of(theirs) {
		t.Fatal("a poll that failed recorded the new version")
	}
	poll(t, env)
	if got := readAll(t, env, "f.bin"); !bytes.Equal(got, theirs) {
		t.Fatal("after the poll that went again the file does not read as the new version")
	}
}

func TestRoundThatCannotReadAnItemAgainUndoesNothing(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	bad := testenv.RandomBytes(5000, 1522)
	writeFile(t, env, "", "bad.txt", bad)
	arm := failReads(env, meta.Ino(env.MustResolve("bad.txt").Ino), 0, 1)
	env.Emu.SetUploadFault(func(name string) int {
		if name != "bad.txt" {
			return 0
		}
		arm()
		return http.StatusForbidden
	})
	rounds(env, 1)
	if names := env.Names(""); len(names) != 1 || names[0] != "bad.txt" {
		t.Fatalf("after a round that could not read the file the root holds %q, want it in place", names)
	}
	if len(env.Logs("nothing is undone now")) == 0 {
		t.Fatal("the log does not tell that nothing is undone")
	}
	env.FS.Store().FailItemReads(nil)
	rounds(env, 2)
	requireShelved(t, env, "", "bad", ".txt", bad)
}

func TestUndoThatCannotReadAWaitingChangeGoesAgain(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Emu.WriteFile(env.Emu.Root(), "new.txt", []byte("new"))
	dst := env.Emu.WriteFile(env.Emu.Root(), "old.txt", []byte("old"))
	env.Start()
	readClosed(t, env, "new.txt")

	ino := meta.Ino(env.MustResolve("old.txt").Ino)
	rename(t, env, "", "new.txt", "", "old.txt")
	arm := failReads(env, ino, 0, 1)
	env.Emu.SetUploadFault(func(string) int {
		arm()
		return http.StatusForbidden
	})
	rounds(env, 1)

	rounds(env, 3)
	if f, _ := env.Emu.Get(dst); f.Trashed || string(f.Content) != "old" {
		t.Fatalf("the file saved over is in Drive's trash %v with %q", f.Trashed, f.Content)
	}
	if got := readAll(t, env, "old.txt"); string(got) != "old" {
		t.Fatalf("the file saved over reads %q, want it back", got)
	}
	if len(env.Logs("read a change that waited for one Drive does not take")) == 0 {
		t.Fatal("the log does not tell of the read that failed")
	}
}

func TestSaveOverThatCannotReadTheFileAfterwardsTellsSo(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id := env.Emu.WriteFile(env.Emu.Root(), "doc.txt", []byte("old"))
	env.Start()
	writeFile(t, env, "", "doc.tmp", []byte("new"))
	ino := meta.Ino(env.MustResolve("doc.tmp").Ino)

	env.FS.Store().FailItemReads(func(it meta.Item) error {
		if it.Ino == ino && it.ID == id {
			return errDiskRead
		}
		return nil
	})
	rename(t, env, "", "doc.tmp", "", "doc.txt")
	env.FS.Store().FailItemReads(nil)
	if len(env.Logs("read the file that took the fileId of the one it was saved over")) == 0 {
		t.Fatal("the log does not tell of the read that failed")
	}
}
