package core_test

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/edited"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestFileNotTakenUpAtAStartGoesUpWhole(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	written := testenv.RandomBytes(100000, 880)
	writeFile(t, env, "", "f.bin", written)
	id := item(t, env, "f.bin").ID
	if err := env.FS.Close(); err != nil {
		t.Fatal(err)
	}
	env.FS = nil

	more := testenv.RandomBytes(50000, 881)
	appendBehindTheDatabase(t, env, id, more)
	want := append(append([]byte(nil), written...), more...)
	var failing atomic.Bool
	failing.Store(true)
	env.Tune = func(o *core.Options) {
		o.RecoverFault = func(step string, _ meta.Item) error {
			if step == edited.StepSize && failing.Load() {
				return syscall.EMFILE
			}
			return nil
		}
	}
	if _, err := env.Open(); err != nil {
		t.Fatalf("the start fails for one file: %v", err)
	}
	rounds(env, 2)
	requireNotUploadedAndFailing(t, env)

	_, err := env.FS.Open(context.Background(), env.MustResolve("f.bin").Ino, syscall.O_RDWR)
	testenv.RequireErrno(t, err, syscall.EIO)
	failing.Store(false)
	uploadAll(t, env)
	if f := inDrive(t, env, "f.bin"); !bytes.Equal(f.Content, want) {
		t.Fatalf("Drive holds %d bytes, want the %d on the disk", len(f.Content), len(want))
	}
	if got := readAll(t, env, "f.bin"); !bytes.Equal(got, want) {
		t.Fatal("the file reads different from what the disk held")
	}
}

func TestTrashingAFileNotTakenUpGoesToDrive(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id := env.Emu.WriteFile(env.Emu.Root(), "f.bin", []byte("in Drive"))
	env.Start()
	fh := openWriter(t, env, "f.bin", 0)
	write(t, env, fh, 0, []byte("edited"))
	env.FS.Release(fh)
	if err := env.FS.Close(); err != nil {
		t.Fatal(err)
	}
	env.FS = nil
	env.Tune = func(o *core.Options) {
		o.RecoverFault = func(step string, _ meta.Item) error {
			if step == edited.StepSize {
				return syscall.EMFILE
			}
			return nil
		}
	}
	if _, err := env.Open(); err != nil {
		t.Fatal(err)
	}
	if err := env.FS.Unlink(env.MustResolve("").Ino, "f.bin"); err != nil {
		t.Fatal(err)
	}
	uploadAll(t, env)
	if f, _ := env.Emu.Get(id); !f.Trashed || string(f.Content) != "in Drive" {
		t.Fatalf("the file in Drive: trashed %v, content %q", f.Trashed, f.Content)
	}
}

func TestDatabaseFailureWhileAFileIsTakenUpStopsTheStart(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	want := editedFromDrive(t, env, 256<<10, 8192, 885)
	if err := env.FS.Close(); err != nil {
		t.Fatal(err)
	}
	env.FS = nil
	fail := true
	env.Tune = func(o *core.Options) {
		o.RecoverFault = func(step string, _ meta.Item) error {
			if step == edited.StepResume && fail {
				return errors.New("the database cannot be read")
			}
			return nil
		}
	}
	if _, err := env.Open(); err == nil {
		t.Fatal("the start went on after the database failed")
	}
	fail = false
	if _, err := env.Open(); err != nil {
		t.Fatal(err)
	}
	env.FS.FillBases(context.Background())
	uploadAll(t, env)
	if f := inDrive(t, env, "f.bin"); !bytes.Equal(f.Content, want) {
		t.Fatal("Drive holds other content than the Drive version with the edit")
	}
}

func TestFileIDGivenBeforeAKillIsFinishedAtTheStart(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	drainIDs(t, env)
	writeFile(t, env, "", "f.txt", []byte("made without a fileId"))
	key := item(t, env, "f.txt").ID
	if !meta.IsLocalKey(key) {
		t.Fatalf("scenario not built: the file has the fileId %q", key)
	}
	env.Emu.SetOffline(false)
	id := giveFileIDAndClose(t, env)
	rebindAsAKillLeftIt(t, env, key, id)
	env.Start()
	requireFileIDFinishedAtStart(t, env, key, id)
}
