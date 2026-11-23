package core_test

import (
	"bytes"
	"context"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestRemovalGoesToTheTrash(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	emu := env.Emu
	file := emu.WriteFile(emu.Root(), "f.txt", []byte("f"))
	dir := emu.Mkdir(emu.Root(), "empty")
	env.Start()
	if err := env.FS.Unlink(env.MustResolve("").Ino, "f.txt"); err != nil {
		t.Fatal(err)
	}
	if err := env.FS.Rmdir(env.MustResolve("").Ino, "empty"); err != nil {
		t.Fatal(err)
	}
	if got := env.Names(""); len(got) != 0 {
		t.Fatalf("root lists %q after removal", got)
	}
	uploadAll(t, env)
	for _, id := range []string{file, dir} {
		if f, ok := emu.Get(id); !ok || !f.Trashed {
			t.Fatalf("%s is not in Drive's trash", id)
		}
	}
}

func TestRmdirOfAFolderWithUncachedFilesFails(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	emu := env.Emu
	full := emu.Mkdir(emu.Root(), "full")
	emu.WriteFile(full, "uncached.bin", []byte("x"))
	drawings := emu.Mkdir(emu.Root(), "drawings")
	emu.Put(emulator.File{Name: "sketch", MimeType: "application/vnd.google-apps.fusiontable",
		Parents: []string{drawings}})
	env.Start()
	for _, name := range []string{"full", "drawings"} {
		err := env.FS.Rmdir(env.MustResolve("").Ino, name)
		testenv.RequireErrno(t, err, syscall.ENOTEMPTY)
	}
	uploadAll(t, env)
	if f, _ := emu.Get(full); f.Trashed {
		t.Fatal("the folder went to the trash")
	}
}

func TestRemovedOpenFileReadsOnUntilClosed(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	content := testenv.RandomBytes(2*big, 60)
	env.Emu.WriteFile(env.Emu.Root(), "f.bin", content)
	env.Start()
	fh := openFile(t, env, "f.bin")
	mustRead(t, env, fh, 0, 10)
	if err := env.FS.Unlink(env.MustResolve("").Ino, "f.bin"); err != nil {
		t.Fatal(err)
	}

	uploadAll(t, env)
	poll(t, env)
	if got := mustRead(t, env, fh, big+1, 10); !bytes.Equal(got, content[big+1:big+11]) {
		t.Fatal("the open handle of a removed file reads wrong bytes")
	}
}

func TestRemovedNewFileIsForgottenWhenClosed(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	ino, fh := createFile(t, env, "", "tmp.txt")
	write(t, env, fh, 0, []byte("scratch"))
	if err := env.FS.Unlink(env.MustResolve("").Ino, "tmp.txt"); err != nil {
		t.Fatal(err)
	}
	if got := mustRead(t, env, fh, 0, 7); string(got) != "scratch" {
		t.Fatalf("the open handle reads %q", got)
	}
	env.FS.Release(fh)
	if _, err := env.FS.Store().ByIno(meta.Ino(ino)); err == nil {
		t.Fatal("a removed file that never reached Drive is still in the database after its close")
	}
	env.Emu.ResetRequests()
	uploadAll(t, env)
	if reqs := env.Emu.Requests(); len(reqs) != 0 {
		t.Fatalf("asked Drive: %+v", reqs)
	}
}

func TestRemovalDuringTheFirstUploadReachesTheTrash(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	writeFile(t, env, "", "f.txt", []byte("f"))
	env.Clock.Advance(time.Minute)
	release := env.Emu.HoldUploads()
	done := make(chan struct{})
	go func() {
		env.FS.SyncOnce(context.Background())
		close(done)
	}()
	waitUntil(t, func() bool { return env.Emu.UploadsWaiting() == 1 })
	if err := env.FS.Unlink(env.MustResolve("").Ino, "f.txt"); err != nil {
		t.Fatal(err)
	}
	release()
	<-done
	uploadAll(t, env)
	poll(t, env)
	for _, f := range env.Emu.Children(env.Emu.Root()) {
		t.Fatalf("Drive keeps %q out of the trash", f.Name)
	}
	if got := env.Names(""); len(got) != 0 {
		t.Fatalf("root lists %q", got)
	}
}

func TestRemovalInsideANewFolderReachesTheTrash(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id := env.Emu.WriteFile(env.Emu.Root(), "f.txt", []byte("f"))
	env.Start()
	mkdir(t, env, "", "new")
	rename(t, env, "", "f.txt", "new", "f.txt")
	if err := env.FS.Unlink(env.MustResolve("new").Ino, "f.txt"); err != nil {
		t.Fatal(err)
	}
	if err := env.FS.Rmdir(env.MustResolve("").Ino, "new"); err != nil {
		t.Fatal(err)
	}
	uploadAll(t, env)
	if f, _ := env.Emu.Get(id); !f.Trashed {
		t.Fatal("the file is not in Drive's trash")
	}
}

func TestRemovingAFileNeverUploadedAsksNothing(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	writeFile(t, env, "", "draft.txt", []byte("d"))
	mkdir(t, env, "", "scratch")
	env.Emu.ResetRequests()
	if err := env.FS.Unlink(env.MustResolve("").Ino, "draft.txt"); err != nil {
		t.Fatal(err)
	}
	if err := env.FS.Rmdir(env.MustResolve("").Ino, "scratch"); err != nil {
		t.Fatal(err)
	}
	uploadAll(t, env)
	if reqs := env.Emu.Requests(); len(reqs) != 0 {
		t.Fatalf("removing never uploaded items asked Drive: %+v", reqs)
	}
}

func TestRemovalAfterALostCreateAnswerReachesTheTrash(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	mkdir(t, env, "", "first")
	writeFile(t, env, "", "f.txt", []byte("content"))
	loseCreates(t, env, "first", "f.txt")
	if err := env.FS.Rmdir(env.MustResolve("").Ino, "first"); err != nil {
		t.Fatal(err)
	}
	if err := env.FS.Unlink(env.MustResolve("").Ino, "f.txt"); err != nil {
		t.Fatal(err)
	}
	uploadAll(t, env)
	poll(t, env)
	for _, name := range []string{"first", "f.txt"} {
		if f := driveItem(t, env, name); !f.Trashed {
			t.Fatalf("%s, created in Drive without an answer and removed here, is not in Drive's trash", name)
		}
	}
	if names := env.Names(""); len(names) != 0 {
		t.Fatalf("the mount shows %v after the removals", names)
	}
}

func TestRemovalOfAnItemKilledMidCreateReachesTheTrash(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	mkdir(t, env, "", "first")

	loseCreates(t, env, "first")
	if err := env.FS.Rmdir(env.MustResolve("").Ino, "first"); err != nil {
		t.Fatal(err)
	}
	env.Restart()
	uploadAll(t, env)
	poll(t, env)
	if f := driveItem(t, env, "first"); !f.Trashed {
		t.Fatal("the folder is alive in Drive after the restart")
	}
	if names := env.Names(""); len(names) != 0 {
		t.Fatalf("the mount shows %v", names)
	}
}

func TestRemovalOfAnItemDriveNeverGotAsksOnceAndForgets(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	mkdir(t, env, "", "first")

	env.Emu.SetUploadFault(func(string) int { return 500 })
	env.Clock.Advance(time.Minute)
	env.FS.SyncOnce(context.Background())
	env.Emu.SetUploadFault(nil)
	if err := env.FS.Rmdir(env.MustResolve("").Ino, "first"); err != nil {
		t.Fatal(err)
	}
	uploadAll(t, env)
	for _, f := range env.Emu.All() {
		if f.Name == "first" {
			t.Fatal("a folder Drive never got is in Drive")
		}
	}
	rows, err := env.FS.Store().Unsent()
	if err != nil || len(rows) != 0 {
		t.Fatalf("the removed folder is still in the database: %v, %v", rows, err)
	}
	if _, err := env.Resolve("first"); err == nil {
		t.Fatal("the removed folder is back in the mount")
	}
}

func TestContentOfAnEditGoesWithTheFileIntoTheTrash(t *testing.T) {
	t.Parallel()
	env, _ := fromDrive(t, 300<<10, 480)
	fh := openWriter(t, env, "f.bin", 0)
	write(t, env, fh, 100, []byte("edit"))
	env.FS.Release(fh)
	id := item(t, env, "f.bin").ID
	if du(t, env) < 300<<10 {
		t.Fatalf("scenario not built: the edited file takes %d bytes on disk", du(t, env))
	}
	if err := env.FS.Unlink(env.MustResolve("").Ino, "f.bin"); err != nil {
		t.Fatal(err)
	}
	uploadAll(t, env)
	if f, _ := env.Emu.Get(id); !f.Trashed || len(env.Emu.Revisions(id)) != 1 {
		t.Fatalf("the file in Drive is trashed %v with %d revisions, want it in the trash as it was", f.Trashed,
			len(env.Emu.Revisions(id)))
	}
	if n := du(t, env); n != 0 {
		t.Fatalf("the content of the dropped edit still takes %d bytes on disk", n)
	}
}
