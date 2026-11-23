package core_test

import (
	"context"
	"slices"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestRenameAndMoveSendOnlyMetadata(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	emu := env.Emu
	a := emu.Mkdir(emu.Root(), "a")
	emu.Mkdir(emu.Root(), "b")
	f := emu.WriteFile(a, "f.txt", []byte("never cached"))
	inner := emu.Mkdir(a, "inner")
	emu.WriteFile(inner, "deep.txt", []byte("deep"))
	env.Start()
	emu.ResetRequests()

	rename(t, env, "a", "f.txt", "b", "g.txt")
	rename(t, env, "", "a", "b", "moved")
	if got := env.Names("b"); !slices.Equal(got, []string{"g.txt", "moved"}) {
		t.Fatalf("b lists %q", got)
	}
	if got := env.Names("b/moved/inner"); !slices.Equal(got, []string{"deep.txt"}) {
		t.Fatalf("the moved folder's subfolder lists %q", got)
	}
	uploadAll(t, env)
	if c, m := uploads(env); c != 0 || m != 2 {
		t.Fatalf("%d content and %d metadata requests, want 0 and 2", c, m)
	}
	if got := mediaRanges(env); len(got) != 0 {
		t.Fatalf("renaming downloaded %q", got)
	}
	b, _ := env.FS.Store().ByIno(meta.Ino(env.MustResolve("b").Ino))
	if got, _ := emu.Get(f); got.Name != "g.txt" || len(got.Parents) != 1 || got.Parents[0] != b.ID {
		t.Fatalf("f.txt in Drive: %q under %v, want g.txt under %s", got.Name, got.Parents, b.ID)
	}
	if string(inDrive(t, env, "b", "moved", "inner", "deep.txt").Content) != "deep" {
		t.Fatal("the moved folder lost its content in Drive")
	}
}

func TestRenameOverAFileBeingWrittenTrashesIt(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	emu := env.Emu
	src := emu.WriteFile(emu.Root(), "new.txt", []byte("new"))
	dst := emu.WriteFile(emu.Root(), "old.txt", []byte("old"))
	env.Start()
	open := openWriter(t, env, "old.txt", 0)
	rename(t, env, "", "new.txt", "", "old.txt")
	if got := mustRead(t, env, open, 0, 3); string(got) != "old" {
		t.Fatalf("the handle of the replaced file reads %q", got)
	}
	env.FS.Release(open)
	if got := env.Names(""); !slices.Equal(got, []string{"old.txt"}) {
		t.Fatalf("root lists %q", got)
	}
	if got := readAll(t, env, "old.txt"); string(got) != "new" {
		t.Fatalf("old.txt reads %q", got)
	}
	uploadAll(t, env)
	if f, _ := emu.Get(src); f.Name != "old.txt" || f.Trashed {
		t.Fatalf("the moved file in Drive: %+v", f.Name)
	}
	if f, ok := emu.Get(dst); !ok || !f.Trashed {
		t.Fatal("the replaced file is not in Drive's trash")
	}
	err := env.FS.Rename(env.MustResolve("").Ino, "old.txt", env.MustResolve("").Ino, "old.txt", unix.RENAME_NOREPLACE)
	if err != nil {
		t.Fatalf("rename onto itself: %v", err)
	}
	writeFile(t, env, "", "third.txt", []byte("3"))
	err = env.FS.Rename(env.MustResolve("").Ino, "third.txt", env.MustResolve("").Ino, "old.txt", unix.RENAME_NOREPLACE)
	testenv.RequireErrno(t, err, syscall.EEXIST)
}

func TestRenameThenRemoveIsOneRequest(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id := env.Emu.WriteFile(env.Emu.Root(), "f.txt", []byte("f"))
	env.Start()
	env.Emu.ResetRequests()
	rename(t, env, "", "f.txt", "", "renamed.txt")
	if err := env.FS.Unlink(env.MustResolve("").Ino, "renamed.txt"); err != nil {
		t.Fatal(err)
	}
	uploadAll(t, env)
	if c, m := uploads(env); c != 0 || m != 1 {
		t.Fatalf("%d content and %d metadata requests, want 0 and 1", c, m)
	}
	if f, _ := env.Emu.Get(id); !f.Trashed {
		t.Fatal("the file is not in Drive's trash")
	}
}

func TestModificationTimeReachesDrive(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id := env.Emu.WriteFile(env.Emu.Root(), "f.txt", []byte("f"))
	env.Start()
	when := time.Date(2020, 2, 3, 4, 5, 6, 0, time.UTC)
	if err := env.FS.SetMtime(env.MustResolve("f.txt").Ino, when); err != nil {
		t.Fatal(err)
	}
	if a := env.MustResolve("f.txt"); !a.Mtime.Equal(when) {
		t.Fatalf("stat shows %v", a.Mtime)
	}
	uploadAll(t, env)
	if f, _ := env.Emu.Get(id); !f.Modified.Equal(when) {
		t.Fatalf("Drive has modifiedTime %v, want %v", f.Modified, when)
	}
}

func TestRenameKeepsRulesOfRename(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	emu := env.Emu
	dir := emu.Mkdir(emu.Root(), "dir")
	emu.Mkdir(dir, "sub")
	emu.WriteFile(emu.Root(), "file", nil)
	full := emu.Mkdir(emu.Root(), "full")
	emu.WriteFile(full, "x", nil)
	env.Start()
	root := env.MustResolve("").Ino
	cases := []struct {
		from, toDir, to string
		want            syscall.Errno
	}{
		{"dir", "dir/sub", "dir", syscall.EINVAL},
		{"dir", "", "file", syscall.ENOTDIR},
		{"file", "", "dir", syscall.EISDIR},
		{"dir", "", "full", syscall.ENOTEMPTY},
	}
	for _, c := range cases {
		err := env.FS.Rename(root, c.from, env.MustResolve(c.toDir).Ino, c.to, 0)
		testenv.RequireErrno(t, err, c.want)
	}
}

func TestRenameAfterALostCreateAnswerReachesDrive(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	mkdir(t, env, "", "first")
	env.Emu.LoseUploadAnswers(1)
	env.Clock.Advance(time.Minute)
	env.FS.SyncOnce(context.Background())
	rename(t, env, "", "first", "", "second")
	uploadAll(t, env)
	inDrive(t, env, "second")
	if it := item(t, env, "second"); it.MetaDirty {
		t.Fatal("the rename is still waiting")
	}
}

func driveItem(t *testing.T, env *testenv.Env, name string) emulator.File {
	t.Helper()
	var hits []emulator.File
	for _, f := range env.Emu.All() {
		if f.Name == name {
			hits = append(hits, f)
		}
	}
	if len(hits) != 1 {
		t.Fatalf("%d items named %q in Drive", len(hits), name)
	}
	return hits[0]
}

func loseCreates(t *testing.T, env *testenv.Env, names ...string) {
	t.Helper()
	env.Emu.LoseUploadAnswers(1000)
	env.Clock.Advance(time.Minute)

	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		env.FS.SyncOnce(ctx)
		close(done)
	}()
	waitUntil(t, func() bool {
		in := 0
		for _, f := range env.Emu.All() {
			if slices.Contains(names, f.Name) {
				in++
			}
		}
		return in == len(names)
	})
	stop()
	<-done
	env.Emu.LoseUploadAnswers(0)
	for _, name := range names {
		if f := driveItem(t, env, name); f.Trashed {
			t.Fatalf("%s is trashed in Drive before its removal", name)
		}
		if it := item(t, env, name); it.RExists {
			t.Fatalf("%s is confirmed here though its answers were lost", name)
		}
	}
}
