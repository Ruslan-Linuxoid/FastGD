package core_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func uploadedUnrecorded(t *testing.T, fails int) (*testenv.Env, []byte) {
	t.Helper()
	env, content := fromDrive(t, 3<<20, 600)
	fh := openWriter(t, env, "f.bin", 0)
	write(t, env, fh, 1<<20, []byte("edit"))
	env.FS.Release(fh)
	copy(content[1<<20:], "edit")
	env.FS.BeforeRecord(func() error {
		if fails > 0 {
			fails--
			return errors.New("the database is busy")
		}
		return nil
	})
	env.Clock.Advance(20 * time.Minute)
	env.FS.SyncOnce(context.Background())
	if f := inDrive(t, env, "f.bin"); !bytes.Equal(f.Content, content) {
		t.Fatal("scenario not built: Drive did not take the upload")
	}
	if it := item(t, env, "f.bin"); !it.Dirty {
		t.Fatal("scenario not built: the database recorded the upload")
	}
	env.Emu.ResetRequests()
	return env, content
}

func TestUploadNotRecordedBeforeARestartIsKnownAfterIt(t *testing.T) {
	t.Parallel()
	env, content := uploadedUnrecorded(t, 1000)
	env.Restart()
	uploadAll(t, env)
	it := item(t, env, "f.bin")
	if it.Dirty || it.LastError != "" || it.Base != 0 || it.MD5 != md5Of(content) {
		t.Fatalf("after the restart the file is dirty %v, base %d, error %q", it.Dirty, it.Base, it.LastError)
	}
	if u := uploadsSeen(t, env); u.simple != 0 {
		t.Fatalf("the content Drive had already went up %d times more", u.simple)
	}
	requireNoLost(t, env)
	if got := readAll(t, env, "f.bin"); !bytes.Equal(got, content) {
		t.Fatal("the file reads different")
	}
}

func TestUploadNotRecordedOfAFileTrashedSinceIsSettled(t *testing.T) {
	t.Parallel()
	env, content := uploadedUnrecorded(t, 1)
	id := item(t, env, "f.bin").ID
	ino := env.MustResolve("f.bin").Ino
	if err := env.FS.Unlink(env.MustResolve("").Ino, "f.bin"); err != nil {
		t.Fatal(err)
	}
	uploadAll(t, env)
	if f, _ := env.Emu.Get(id); !f.Trashed || !bytes.Equal(f.Content, content) {
		t.Fatalf("Drive holds the file trashed=%v", f.Trashed)
	}
	if names := env.Names(""); len(names) != 0 {
		t.Fatalf("the removed file is back: %q", names)
	}
	if ops, err := env.FS.Store().Ops(meta.Ino(ino)); err != nil || len(ops) != 0 {
		t.Fatalf("the trashed file is left with the operations %+v, %v", ops, err)
	}
}

func TestForeignVersionAfterAnUploadNotRecordedIsAConflict(t *testing.T) {
	t.Parallel()
	env, content := uploadedUnrecorded(t, 1)
	id := item(t, env, "f.bin").ID
	theirs := testenv.RandomBytes(5000, 601)
	env.Emu.Update(id, func(f *emulator.File) { f.Content = theirs })
	poll(t, env)
	uploadAll(t, env)
	if got := readAll(t, env, "f.bin"); !bytes.Equal(got, theirs) {
		t.Fatal("the file does not read as the newer version of Drive")
	}
	if f, _ := env.Emu.Get(id); !bytes.Equal(f.Content, theirs) {
		t.Fatal("Drive's newer version changed")
	}
	requireShelved(t, env, "", "f", ".bin", content)
}
