package core_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
	"google.golang.org/api/googleapi"
)

func TestDriveRefusesWhatTheRightsDeny(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	ctx := context.Background()
	root := env.Emu.Root()
	closed := env.Emu.Put(emulator.File{Name: "closed", MimeType: emulator.MimeFolder, Parents: []string{root},
		CannotRemoveChildren: true})
	inside := env.Emu.Put(emulator.File{Name: "in.txt", MimeType: "text/plain", Parents: []string{closed}})
	id := foreign(env, "theirs.txt", func(f *emulator.File) {
		f.CannotModify, f.CannotTrash, f.CannotRename = true, true, true
	})
	calls := map[string]func() error{
		"content": func() error {
			_, err := env.Drive.UpdateContent(ctx, id, "text/plain", time.Now(), 4,
				func() io.Reader { return strings.NewReader("mine") })
			return err
		},
		"time": func() error {
			_, err := env.Drive.UpdateMeta(ctx, id, drive.MetaUpdate{Name: "theirs.txt", Modified: time.Now()})
			return err
		},
		"trash": func() error {
			_, err := env.Drive.UpdateMeta(ctx, id, drive.MetaUpdate{Trashed: true})
			return err
		},
		"rename": func() error {
			_, err := env.Drive.UpdateMeta(ctx, id, drive.MetaUpdate{Name: "mine.txt"})
			return err
		},
		"move out": func() error {
			_, err := env.Drive.UpdateMeta(ctx, inside, drive.MetaUpdate{OldParent: closed, NewParent: root})
			return err
		},
	}
	for name, call := range calls {
		var gerr *googleapi.Error
		if err := call(); !errors.As(err, &gerr) || gerr.Code != 403 {
			t.Fatalf("%s against the rights got %v, want 403", name, err)
		}
	}
}

func TestFileThatMayNotBeDownloadedRefusesReads(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	foreign(env, "open.txt", func(*emulator.File) {})
	closed := foreign(env, "closed.txt", func(f *emulator.File) { f.CannotDownload = true })
	later := foreign(env, "later.txt", func(*emulator.File) {})
	env.Start()
	if a, b := env.MustResolve("open.txt"), env.MustResolve("closed.txt"); a.Mode != b.Mode || a.Size != b.Size {
		t.Fatalf("the file that may not be downloaded shows the mode %o and %d bytes, the other %o and %d", b.Mode, b.Size,
			a.Mode, a.Size)
	}
	env.Emu.ResetRequests()
	for _, flags := range []uint32{syscall.O_RDONLY, syscall.O_RDWR} {
		_, err := env.FS.Open(context.Background(), env.MustResolve("closed.txt").Ino, flags)
		testenv.RequireErrno(t, err, syscall.EACCES)
	}
	if got := env.Emu.Requests(); len(got) != 0 {
		t.Fatalf("Drive got %d requests for a file that may not be downloaded, the first %+v", len(got), got[0])
	}
	if got := readAll(t, env, "open.txt"); string(got) != "theirs" {
		t.Fatalf("the other file reads %q", got)
	}

	env.Emu.Update(later, func(f *emulator.File) { f.CannotDownload = true })
	env.Emu.Update(closed, func(f *emulator.File) { f.CannotDownload = false })
	poll(t, env)
	if got := readAll(t, env, "closed.txt"); string(got) != "theirs" {
		t.Fatalf("after the owner allowed the download the file reads %q", got)
	}
	_, err := env.FS.Open(context.Background(), env.MustResolve("later.txt").Ino, syscall.O_RDONLY)
	testenv.RequireErrno(t, err, syscall.EACCES)

	testenv.RequireErrno(t, env.FS.Truncate(context.Background(), env.MustResolve("later.txt").Ino, 3), syscall.EACCES)
	_, err = env.FS.Open(context.Background(), env.MustResolve("later.txt").Ino, syscall.O_WRONLY)
	testenv.RequireErrno(t, err, syscall.EACCES)
	fh, err := env.FS.Open(context.Background(), env.MustResolve("later.txt").Ino, syscall.O_WRONLY|syscall.O_TRUNC)
	if err != nil {
		t.Fatalf("a write that starts the file anew: %v", err)
	}
	write(t, env, fh, 0, []byte("mine"))
	env.FS.Release(fh)
	if got := readAll(t, env, "later.txt"); string(got) != "mine" {
		t.Fatalf("the file written anew reads %q", got)
	}
}

func TestDriveRefusesTheDownloadTheOwnerForbids(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id := foreign(env, "closed.txt", func(f *emulator.File) { f.CannotDownload = true })
	err := env.Drive.Stream(context.Background(), id, 0, 6, func([]byte) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "cannotDownloadFile") {
		t.Fatalf("the download of a file that may not be downloaded ended with %v", err)
	}
}
