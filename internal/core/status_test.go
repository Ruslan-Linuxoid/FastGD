package core_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/cache"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestStatFsWithoutALimitShowsAPetabyteFree(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	if total, free := env.FS.StatFs(); free != 1<<50 || total != 1<<50 {
		t.Fatalf("before Drive reports a quota StatFs = %d total, %d free", total, free)
	}
	env.Emu.WriteFile(env.Emu.Root(), "f", make([]byte, 100))
	if err := env.FS.RefreshQuota(context.Background()); err != nil {
		t.Fatal(err)
	}
	if total, free := env.FS.StatFs(); free != 1<<50 || total != 100+(1<<50) {
		t.Fatalf("without a limit StatFs = %d total, %d free", total, free)
	}
}

func TestStatFsShowsTheDriveQuota(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Emu.SetQuota(15<<30, 4<<30)
	env.Start()
	if err := env.FS.RefreshQuota(context.Background()); err != nil {
		t.Fatal(err)
	}
	if total, free := env.FS.StatFs(); total != 15<<30 || free != 11<<30 {
		t.Fatalf("StatFs = %d total, %d free", total, free)
	}

	env.Emu.SetOffline(true)
	env.Restart()
	if total, free := env.FS.StatFs(); total != 15<<30 || free != 11<<30 {
		t.Fatalf("after a restart StatFs = %d total, %d free", total, free)
	}
}

func requireDirtyShownBesideDaemon(t *testing.T, env *testenv.Env, ro *meta.ReadOnly) {
	t.Helper()
	st, err := ro.ReadStatus(env.Clock.Now().Add(-24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	held, err := cache.Held(ro)
	if err != nil {
		t.Fatal(err)
	}
	if st.DirtyFiles != 2 || st.DirtyBytes != 7 || held != 1007 {
		t.Fatalf("status: %d dirty files of %d bytes, %d cached", st.DirtyFiles, st.DirtyBytes, held)
	}
	if !st.LastPoll.Equal(env.Clock.Now()) {
		t.Fatalf("last poll %v, want %v", st.LastPoll, env.Clock.Now())
	}
}

func requireFailingShownBesideDaemon(t *testing.T, env *testenv.Env, ro *meta.ReadOnly) {
	t.Helper()
	env.Clock.Advance(time.Minute)
	env.FS.SyncOnce(context.Background())
	st, err := ro.ReadStatus(env.Clock.Now().Add(-24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Failing) != 1 || st.Failing[0].Path != "My Drive/stuck.txt" || st.Failing[0].LastError == "" ||
		st.Failing[0].NextTry.IsZero() {
		t.Fatalf("failing: %+v", st.Failing)
	}
}

func TestStatusReadsBesideTheDaemon(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Emu.WriteFile(env.Emu.Root(), "cached.bin", testenv.RandomBytes(1000, 70))
	env.Start()
	readAll(t, env, "cached.bin")
	writeFile(t, env, "", "dirty.txt", []byte("12345"))
	writeFile(t, env, "", "stuck.txt", []byte("67"))
	env.Emu.SetUploadFault(func(name string) int {
		if name == "stuck.txt" {
			return 503
		}
		return 0
	})

	env.Clock.Advance(2 * time.Minute)
	poll(t, env)

	ro, err := meta.OpenReadOnly(filepath.Join(env.Dir, "meta.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	requireDirtyShownBesideDaemon(t, env, ro)
	requireFailingShownBesideDaemon(t, env, ro)
}

func requireTrashingWaits(t *testing.T, env *testenv.Env) {
	t.Helper()
	if st := readStatus(t, env); st.DirtyFiles != 0 || st.DirtyBytes != 0 || st.Waiting == 0 {
		t.Fatalf("with the changed file deleted here status has %d dirty files of %d bytes, and %d operations wait; want "+
			"none dirty and the trashing waiting", st.DirtyFiles, st.DirtyBytes, st.Waiting)
	}
}

func requireNothingWaits(t *testing.T, env *testenv.Env) {
	t.Helper()
	if st := readStatus(t, env); st.DirtyFiles != 0 || st.Waiting != 0 || len(st.Failing) != 0 {
		t.Fatalf("after the trashing went status has %d dirty files, %d operations waiting, %d failing", st.DirtyFiles,
			st.Waiting, len(st.Failing))
	}
}

func TestFileChangedAndDeletedHereIsNoDirtyFile(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id := env.Emu.WriteFile(env.Emu.Root(), "f.txt", []byte("as Drive has it"))
	env.Start()
	fh := openWriter(t, env, "f.txt", 0)
	write(t, env, fh, 0, []byte("changed here"))
	env.FS.Release(fh)
	if st := readStatus(t, env); st.DirtyFiles != 1 || st.Waiting != 1 {
		t.Fatalf("scenario not built: %d dirty files, %d operations wait", st.DirtyFiles, st.Waiting)
	}
	if err := env.FS.Unlink(env.MustResolve("").Ino, "f.txt"); err != nil {
		t.Fatal(err)
	}
	requireTrashingWaits(t, env)
	uploadAll(t, env)
	if f, _ := env.Emu.Get(id); !f.Trashed || string(f.Content) != "as Drive has it" {
		t.Fatalf("Drive has the file trashed %v with the content %q", f.Trashed, f.Content)
	}
	requireNothingWaits(t, env)
}
