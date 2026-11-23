package core_test

import (
	"context"
	"slices"
	"syscall"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func requireRenamedOverInDrive(t *testing.T, env *testenv.Env, src, dst string, content []byte) {
	t.Helper()
	if f, _ := env.Emu.Get(dst); f.Trashed || f.Name != "old.bin" || !slices.Equal(f.Content, content) {
		t.Fatalf("the file renamed over: trashed %v, named %q, %d bytes", f.Trashed, f.Name, len(f.Content))
	}
	if got := len(env.Emu.Revisions(dst)); got != 2 {
		t.Fatalf("the file renamed over has %d revisions, want 2", got)
	}
	if f, _ := env.Emu.Get(src); !f.Trashed || f.Name != "new.bin" || len(env.Emu.Revisions(src)) != 1 {
		t.Fatalf("the renamed file in Drive: trashed %v, named %q", f.Trashed, f.Name)
	}
}

func TestCachedFileOfDriveRenamedOverAnotherGivesItsContent(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	content := testenv.RandomBytes(3<<20, 7)
	src := env.Emu.WriteFile(env.Emu.Root(), "new.bin", content)
	dst := env.Emu.WriteFile(env.Emu.Root(), "old.bin", []byte("old"))
	env.Start()
	readClosed(t, env, "new.bin")
	env.Emu.ResetRequests()
	rename(t, env, "", "new.bin", "", "old.bin")
	if got := mediaRanges(env); len(got) != 0 {
		t.Fatalf("the rename downloaded %q", got)
	}
	if got := env.Names(""); !slices.Equal(got, []string{"old.bin"}) {
		t.Fatalf("root lists %q", got)
	}
	if got := readAll(t, env, "old.bin"); !slices.Equal(got, content) {
		t.Fatal("old.bin does not read as the file renamed over it")
	}
	uploadAll(t, env)
	requireRenamedOverInDrive(t, env, src, dst, content)
	env.Restart()
	if got := readAll(t, env, "old.bin"); !slices.Equal(got, content) {
		t.Fatal("after a restart old.bin does not read as the file renamed over it")
	}
}

func TestFileOfDriveNotCachedIsRenamedWithNoDownload(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	content := testenv.RandomBytes(3<<20, 8)
	src := env.Emu.WriteFile(env.Emu.Root(), "new.bin", content)
	dst := env.Emu.WriteFile(env.Emu.Root(), "old.bin", []byte("old"))
	env.Start()

	fh := openFile(t, env, "new.bin")
	mustRead(t, env, fh, 0, 16<<10)
	env.FS.Release(fh)
	waitUntil(t, func() bool { return cached(t, env, "new.bin") > 0 })
	env.Emu.ResetRequests()
	rename(t, env, "", "new.bin", "", "old.bin")
	if got := env.Emu.Requests(); len(got) != 0 {
		t.Fatalf("the rename asked Drive %d times, the first %+v", len(got), got[0])
	}
	if it := item(t, env, "old.bin"); it.ID != src || it.Dirty {
		t.Fatalf("old.bin is the file %s, changed here: %v; want %s as it is in Drive", it.ID, it.Dirty, src)
	}
	uploadAll(t, env)
	if f, _ := env.Emu.Get(dst); !f.Trashed {
		t.Fatal("the replaced file is not in Drive's trash")
	}
	if f, _ := env.Emu.Get(src); f.Name != "old.bin" || f.Trashed || len(env.Emu.Revisions(src)) != 1 {
		t.Fatalf("the renamed file in Drive: %q, trashed %v, %d revisions", f.Name, f.Trashed, len(env.Emu.Revisions(src)))
	}
}

func TestRenamedFileOfDriveStaysUntilItsContentIsThere(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	src := env.Emu.WriteFile(env.Emu.Root(), "new.txt", []byte("new"))
	dst := env.Emu.WriteFile(env.Emu.Root(), "old.txt", []byte("old"))
	env.Start()
	readClosed(t, env, "new.txt")
	rename(t, env, "", "new.txt", "", "old.txt")

	env.Emu.SetStorageFull(true)
	rounds(env, 3)
	if f, _ := env.Emu.Get(src); f.Trashed {
		t.Fatal("the renamed file is in Drive's trash while its content is not in Drive under the other fileId")
	}
	env.Emu.SetStorageFull(false)
	uploadAll(t, env)
	if f, _ := env.Emu.Get(src); !f.Trashed {
		t.Fatal("the renamed file is not in Drive's trash")
	}
	if f, _ := env.Emu.Get(dst); string(f.Content) != "new" || f.Trashed {
		t.Fatalf("the file renamed over holds %q, trashed %v", f.Content, f.Trashed)
	}
}

func TestRenameOverWithoutDriveReplaces(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	src := env.Emu.WriteFile(env.Emu.Root(), "new.txt", []byte("new"))
	dst := env.Emu.WriteFile(env.Emu.Root(), "old.txt", []byte("old"))
	env.Start()
	env.Emu.SetOffline(true)
	rename(t, env, "", "new.txt", "", "old.txt")
	if it := item(t, env, "old.txt"); it.ID != src || it.Dirty {
		t.Fatalf("old.txt is the file %s, changed here: %v; want %s as it is in Drive", it.ID, it.Dirty, src)
	}
	env.Emu.SetOffline(false)
	uploadAll(t, env)
	if f, _ := env.Emu.Get(dst); !f.Trashed {
		t.Fatal("the replaced file is not in Drive's trash")
	}
	if f, _ := env.Emu.Get(src); f.Name != "old.txt" || f.Trashed || len(env.Emu.Revisions(src)) != 1 {
		t.Fatalf("the renamed file in Drive: %q, trashed %v, %d revisions", f.Name, f.Trashed, len(env.Emu.Revisions(src)))
	}
}

func TestRenameOverAFileBeingUploadedReplacesIt(t *testing.T) {
	env := testenv.New(t)
	id := env.Emu.WriteFile(env.Emu.Root(), "doc.txt", []byte("old"))
	env.Start()
	fh := openWriter(t, env, "doc.txt", syscall.O_TRUNC)
	write(t, env, fh, 0, []byte("edited here"))
	env.FS.Release(fh)
	release := env.Emu.HoldUploads()
	done := make(chan struct{})
	go func() {
		env.FS.SyncOnce(context.Background())
		close(done)
	}()
	waitUntil(t, func() bool { return env.Emu.UploadsWaiting() == 1 })
	writeFile(t, env, "", "doc.tmp", []byte("saved"))
	rename(t, env, "", "doc.tmp", "", "doc.txt")
	if it := item(t, env, "doc.txt"); it.ID == id {
		t.Fatal("the file took the fileId of one whose upload is under way")
	}
	release()
	<-done
	uploadAll(t, env)
	if f, _ := env.Emu.Get(id); !f.Trashed {
		t.Fatal("the replaced file is not in Drive's trash")
	}
	if f := inDrive(t, env, "doc.txt"); f.ID == id || string(f.Content) != "saved" {
		t.Fatalf("doc.txt in Drive is the file %s with %q", f.ID, f.Content)
	}
}
