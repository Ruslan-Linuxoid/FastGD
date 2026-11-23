package core_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/edited"
	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestAnyChangeOfNamesTellsTheKernel(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	ids := dups(env)
	env.Start()
	rec := &recorder{}
	env.FS.SetNotifier(rec)
	root := env.MustResolve("").Ino
	store := env.FS.Store()
	oldest, err := store.ByID(ids[0])
	if err != nil {
		t.Fatal(err)
	}

	failed := errors.New("the change did not happen")
	err = env.FS.ChangeShown(func(meta.Visible) error { return failed }, oldest)
	env.FS.WaitTold()
	if !errors.Is(err, failed) || len(rec.entries)+len(rec.inodes) != 0 {
		t.Fatalf("a change that failed gave %v and told the kernel %q %q", err, rec.entries, rec.inodes)
	}

	placed := oldest
	placed.Name = "first.jpg"
	err = env.FS.ChangeShown(func(v meta.Visible) error { return v.Rename(oldest, oldest.Parent, "first.jpg", nil) },
		oldest, placed)
	if err != nil {
		t.Fatal(err)
	}
	env.FS.WaitTold()
	if !heard(rec, root, "IMG.jpg", "first.jpg") {
		t.Fatalf("the kernel heard only %q", rec.entries)
	}
}

func TestRightsLoadedAfterAnUpgradeTellTheKernel(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id := env.Emu.WriteFile(env.Emu.Root(), "shared.txt", []byte("x"))
	env.Emu.WriteFile(env.Emu.Root(), "mine.txt", []byte("y"))
	env.Start()
	env.Emu.Update(id, func(f *emulator.File) { f.CannotModify = true })
	if err := env.FS.Store().SetState(meta.StateRightsUnknown, "1"); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	env.FS.SetNotifier(rec)
	shared, mine := env.MustResolve("shared.txt").Ino, env.MustResolve("mine.txt").Ino
	env.FS.LoadRights(context.Background())
	if a := env.MustResolve("shared.txt"); a.Mode&0o777 != 0o444 {
		t.Fatalf("scenario not built: the mode is %o after the rights were read", a.Mode&0o777)
	}
	env.FS.WaitTold()
	if !toldOf(rec, shared) {
		t.Fatalf("the kernel was not told of the file whose rights changed: %q", rec.inodes)
	}
	if toldOf(rec, mine) {
		t.Fatal("the kernel was told of a file whose rights are as they were")
	}
}

func TestUndoneTimeTellsTheKernel(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Emu.WriteFile(env.Emu.Root(), "a.txt", []byte("x"))
	env.Emu.SetUploadFault(func(string) int { return http.StatusBadRequest })
	env.Start()
	a := env.MustResolve("a.txt")
	if err := env.FS.SetMtime(a.Ino, a.Mtime.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	rec := &recorder{}
	env.FS.SetNotifier(rec)
	rounds(env, 6)
	if now := env.MustResolve("a.txt"); !now.Mtime.Equal(a.Mtime) {
		t.Fatalf("scenario not built: the time is %v, Drive's is %v", now.Mtime, a.Mtime)
	}
	env.FS.WaitTold()
	if !toldOf(rec, a.Ino) {
		t.Fatalf("the kernel was not told of the file whose time went back: %q", rec.inodes)
	}
}

func TestLocalVersionInLostAndFoundTellsItsMode(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id, _ := report(t, env)
	ino := env.MustResolve("Отчёты/2026/a.odt").Ino
	env.Emu.Update(id, func(f *emulator.File) { f.CannotModify = true })
	poll(t, env)
	if a := env.MustResolve("Отчёты/2026/a.odt"); a.Mode&0o777 != 0o444 {
		t.Fatalf("scenario not built: the mode is %o after the owner took the right", a.Mode&0o777)
	}
	rec := &recorder{}
	env.FS.SetNotifier(rec)
	env.Emu.Update(id, func(f *emulator.File) { f.Content = []byte("theirs") })
	poll(t, env)
	names := lostNames(env, lostDir+"/Отчёты/2026")
	if len(names) != 1 {
		t.Fatalf("scenario not built: Lost and Found holds %q", names)
	}
	lost := env.MustResolve(lostDir + "/Отчёты/2026/" + names[0])
	if lost.Ino != ino || lost.Mode&0o777 != 0o644 {
		t.Fatalf("scenario not built: the local version has the inode %d, mode %o", lost.Ino, lost.Mode&0o777)
	}
	env.FS.WaitTold()

	told := 0
	for _, n := range rec.inodes {
		if n == fmtInode(ino, false) {
			told++
		}
	}
	if told < 2 {
		t.Fatalf("the kernel was not told of the mode of the local version after the move: %q", rec.inodes)
	}
}

func TestVisibleChangesAreTakenOnlyByTheNotifier(t *testing.T) {
	t.Parallel()
	for _, dir := range []string{".", "../edited", "../cache", "../fusefs", "../../cmd/fastgd"} {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil || len(files) == 0 {
			t.Fatalf("no sources in %s: %v", dir, err)
		}
		for _, p := range files {
			if strings.HasSuffix(p, "_test.go") || p == "shown.go" {
				continue
			}
			src, err := os.ReadFile(p)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(src, []byte(".Visible()")) {
				t.Errorf("%s takes the visible changes of the store itself", p)
			}
		}
	}
}

func TestSizeTakenUpAfterTheStartTellsTheKernel(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	writeFile(t, env, "", "f.bin", testenv.RandomBytes(100000, 890))
	id := item(t, env, "f.bin").ID
	if err := env.FS.Close(); err != nil {
		t.Fatal(err)
	}
	env.FS = nil

	fd, err := os.OpenFile(contentFile(env, id), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fd.Write(testenv.RandomBytes(50000, 891)); err != nil {
		t.Fatal(err)
	}
	fd.Close()
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
		t.Fatal(err)
	}
	rec := &recorder{}
	env.FS.SetNotifier(rec)
	a := env.MustResolve("f.bin")
	if a.Size != 100000 {
		t.Fatalf("scenario not built: the size is %d before the file is taken up", a.Size)
	}
	failing.Store(false)
	uploadAll(t, env)
	if now := env.MustResolve("f.bin"); now.Size != 150000 {
		t.Fatalf("scenario not built: the size is %d after the file was taken up", now.Size)
	}
	env.FS.WaitTold()
	if !toldOf(rec, a.Ino) {
		t.Fatalf("the kernel was not told of the file whose size changed: %q", rec.inodes)
	}
}
