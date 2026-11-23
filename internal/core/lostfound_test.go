package core_test

import (
	"bytes"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestStartFinishesAMoveToLostAndFoundThatAKillCut(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id, local := report(t, env)
	theirs := testenv.RandomBytes(64<<10, 440)
	env.Emu.Update(id, func(f *emulator.File) { f.Content = theirs })
	poll(t, env)
	as := requireShelved(t, env, "Отчёты/2026", "a", ".odt", local)
	moved := item(t, env, lostDir+"/Отчёты/2026/"+as).ID
	if err := env.FS.Close(); err != nil {
		t.Fatal(err)
	}
	env.FS = nil
	content := func(id string) string { return contentFile(env, id) }
	if err := os.MkdirAll(filepath.Dir(content(id)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(content(moved), content(id)); err != nil {
		t.Fatal(err)
	}
	if err := execSQL(env.Dir+"/meta.db", `INSERT INTO shelf(took, had) VALUES ('`+moved+`', '`+id+`')`); err != nil {
		t.Fatal(err)
	}
	env.Start()
	if got := readAll(t, env, "Отчёты/2026/a.odt"); !bytes.Equal(got, theirs) {
		t.Fatal("the file does not read as Drive's version")
	}
	requireShelved(t, env, "Отчёты/2026", "a", ".odt", local)
}

func requireTheirsUnderItsOwnName(t *testing.T, env *testenv.Env, id string, theirs []byte) {
	t.Helper()
	if got := readAll(t, env, "Отчёты/2026/a.odt"); !bytes.Equal(got, theirs) {
		t.Fatal("the file does not read as Drive's version under its own name")
	}
	if it := item(t, env, "Отчёты/2026/a.odt"); it.Dirty || it.ID != id {
		t.Fatalf("the file under its own name is dirty=%v with the fileId %s", it.Dirty, it.ID)
	}
}

func requireLocalShelvedApart(t *testing.T, env *testenv.Env, id string, theirs, local []byte) {
	t.Helper()
	as := requireShelved(t, env, "Отчёты/2026", "a", ".odt", local)
	if f, _ := env.Emu.Get(id); !bytes.Equal(f.Content, theirs) {
		t.Fatal("Drive's version changed")
	}
	if left, err := env.FS.Store().Shelf().Unfinished(); err != nil || len(left) != 0 {
		t.Fatalf("moves are left unfinished: %v %v", left, err)
	}
	lost := item(t, env, lostDir+"/Отчёты/2026/"+as).ID
	mine, err := os.Stat(contentFile(env, lost))
	if err != nil {
		t.Fatal(err)
	}
	if drives, err := os.Stat(contentFile(env, id)); err == nil && os.SameFile(mine, drives) {
		t.Fatal("the cache holds the local version under the fileId of Drive's version too")
	}
}

func TestKillAfterAnyStepOfAMoveIntoLostAndFoundLosesNothing(t *testing.T) {
	t.Parallel()
	for _, step := range core.ShelveSteps() {
		t.Run(step, func(t *testing.T) {
			t.Parallel()
			env := testenv.New(t)
			killed := killAfter(env, step)
			id, local := report(t, env)
			theirs := testenv.RandomBytes(10000, 403)
			env.Emu.Update(id, func(f *emulator.File) { f.Content = theirs })
			poll(t, env)
			if !killed.Load() {
				t.Fatal("scenario not built: the move did not reach the step")
			}
			env.Tune = nil
			env.Restart()
			rounds(env, 2)
			requireTheirsUnderItsOwnName(t, env, id, theirs)
			requireLocalShelvedApart(t, env, id, theirs, local)
		})
	}
}

func killAfter(env *testenv.Env, step string) *atomic.Bool {
	killed := &atomic.Bool{}
	env.Tune = func(o *core.Options) {
		o.ShelveFault = func(at string) error {
			if at == step {
				killed.Store(true)
			}
			if killed.Load() {
				return errors.New("killed")
			}
			return nil
		}
	}
	return killed
}

func TestKillAfterTheMoveOfAFileDriveNeverTookLosesNothing(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Emu.SetUploadFault(func(name string) int {
		if name == "bad.txt" {
			return http.StatusBadRequest
		}
		return 0
	})
	killed := killAfter(env, "rows moved")
	env.Start()
	bad := testenv.RandomBytes(5000, 404)
	writeFile(t, env, "", "bad.txt", bad)
	rounds(env, 5)
	if !killed.Load() {
		t.Fatal("scenario not built: the move did not reach the step")
	}
	env.Tune = nil
	env.Restart()
	rounds(env, 2)
	if names := env.Names(""); len(names) != 1 || names[0] != lostDir {
		t.Fatalf("the root holds %q, want Lost and Found alone", names)
	}
	requireShelved(t, env, "", "bad", ".txt", bad)
	for _, f := range env.Emu.All() {
		if f.Name != "" && f.ID != env.Emu.Root() {
			t.Fatalf("%q went to Drive", f.Name)
		}
	}
}

func TestKillAfterGivingWayToDriveLeavesTheFileReadable(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id := env.Emu.WriteFile(env.Emu.Root(), "f.bin", testenv.RandomBytes(64<<10, 1450))
	killed := killAfter(env, core.TakeRemoteStep)
	env.Start()
	fh := openWriter(t, env, "f.bin", 0)
	write(t, env, fh, 100, []byte("edit"))
	env.FS.Release(fh)
	if err := env.FS.Unlink(env.MustResolve("").Ino, "f.bin"); err != nil {
		t.Fatal(err)
	}
	theirs := testenv.RandomBytes(32<<10, 1451)
	env.Emu.Update(id, func(f *emulator.File) { f.Content = theirs })
	rounds(env, 3)
	if !killed.Load() {
		t.Fatal("scenario not built: the giving way did not reach the step")
	}
	env.Tune = nil
	env.Restart()
	rounds(env, 2)
	poll(t, env)
	if got := readAll(t, env, "f.bin"); !bytes.Equal(got, theirs) {
		t.Fatal("after the start the file does not read as Drive's version")
	}
	if it := item(t, env, "f.bin"); it.Dirty || it.LastError != "" {
		t.Fatalf("the file is changed here %v with the error %q", it.Dirty, it.LastError)
	}
}
