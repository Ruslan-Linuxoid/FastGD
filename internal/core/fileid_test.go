package core_test

import (
	"bytes"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func makeKeyedTree(t *testing.T, env *testenv.Env) []string {
	t.Helper()
	paths := make([]string, 0, 13)
	paths = append(paths, "tree")
	mkdir(t, env, "", "tree")
	for i := range 6 {
		dir := fmt.Sprintf("d%d", i)
		mkdir(t, env, "tree", dir)
		writeFile(t, env, "tree/"+dir, "f.txt", fmt.Appendf(nil, "content %d", i))
		paths = append(paths, "tree/"+dir, "tree/"+dir+"/f.txt")
	}
	for _, p := range paths {
		if it := item(t, env, p); !meta.IsLocalKey(it.ID) || it.RExists {
			t.Fatalf("%s is made under %q, known in Drive %v; want a key of its own", p, it.ID, it.RExists)
		}
	}
	return paths
}

func requireTreeUploaded(t *testing.T, env *testenv.Env, paths []string) {
	t.Helper()
	env.Emu.SetOffline(false)
	uploadAll(t, env)
	for i := range 6 {
		dir := fmt.Sprintf("d%d", i)
		f := inDrive(t, env, "tree", dir, "f.txt")
		if want := fmt.Sprintf("content %d", i); string(f.Content) != want {
			t.Fatalf("Drive holds %q in %s, want %q", f.Content, dir, want)
		}
		it := item(t, env, "tree/"+dir+"/f.txt")
		if it.ID != f.ID || meta.IsLocalKey(it.ID) || it.Dirty {
			t.Fatalf("the file of %s has the fileId %q here and %q in Drive, dirty %v", dir, it.ID, f.ID, it.Dirty)
		}
	}
	for _, p := range paths {
		if it := item(t, env, p); meta.IsLocalKey(it.ID) || meta.IsLocalKey(it.Parent) {
			t.Fatalf("after the upload %s lives under %q in %q", p, it.ID, it.Parent)
		}
	}
}

func requireHandlesAcrossUpload(t *testing.T, env *testenv.Env, fh, wh uint64) {
	t.Helper()
	if got := mustRead(t, env, fh, 0, 100); string(got) != "content 5" {
		t.Fatalf("the handle opened before the file took its fileId reads %q", got)
	}

	write(t, env, wh, 0, []byte("CONTENT"))
	if got := mustRead(t, env, wh, 0, 100); string(got) != "CONTENT 4" {
		t.Fatalf("the handle opened for writing before the file took its fileId reads %q", got)
	}
	env.FS.Release(wh)
	uploadAll(t, env)
	if f := inDrive(t, env, "tree", "d4", "f.txt"); string(f.Content) != "CONTENT 4" {
		t.Fatalf("Drive holds %q after the write through the handle", f.Content)
	}
}

func requireNothingUnfinished(t *testing.T, env *testenv.Env) {
	t.Helper()
	if left, err := env.FS.Store().Shelf().Unfinished(); err != nil || len(left) != 0 {
		t.Fatalf("changes of fileIds are left unfinished: %v %v", left, err)
	}
	if n, err := env.FS.Store().CountOps(); err != nil || n != 0 {
		t.Fatalf("%d operations are left after the upload: %v", n, err)
	}
}

func requireReadsAfterRestart(t *testing.T, env *testenv.Env) {
	t.Helper()
	env.Restart()
	for i := range 6 {
		p := fmt.Sprintf("tree/d%d/f.txt", i)
		want := fmt.Sprintf("content %d", i)
		if i == 4 {
			want = "CONTENT 4"
		}
		if got := readAll(t, env, p); string(got) != want {
			t.Fatalf("after a restart %s reads %q", p, got)
		}
	}
}

func TestNewItemsLiveUnderKeysUntilTheyReachDrive(t *testing.T) {
	t.Parallel()
	for name, offline := range map[string]bool{"with the network": false, "without it and with no fileId in store": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			env := testenv.New(t)
			env.Start()
			if offline {
				drainIDs(t, env)
			}
			paths := makeKeyedTree(t, env)
			fh := openFile(t, env, "tree/d5/f.txt")
			defer env.FS.Release(fh)
			wh := openWriter(t, env, "tree/d4/f.txt", 0)

			requireTreeUploaded(t, env, paths)
			requireHandlesAcrossUpload(t, env, fh, wh)
			requireNothingUnfinished(t, env)
			requireReadsAfterRestart(t, env)
		})
	}
}

func TestItemsUnderKeysGoToDriveAfterARestart(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	env.Emu.SetOffline(true)
	mkdir(t, env, "", "dir")
	writeFile(t, env, "dir", "f.txt", []byte("made before the restart"))
	env.Restart()
	for _, p := range []string{"dir", "dir/f.txt"} {
		if it := item(t, env, p); !meta.IsLocalKey(it.ID) {
			t.Fatalf("after the restart %s lives under %q, want its key", p, it.ID)
		}
	}
	if got := readAll(t, env, "dir/f.txt"); string(got) != "made before the restart" {
		t.Fatalf("after the restart the file reads %q", got)
	}
	env.Emu.SetOffline(false)
	uploadAll(t, env)
	f := inDrive(t, env, "dir", "f.txt")
	if it := item(t, env, "dir/f.txt"); string(f.Content) != "made before the restart" || it.ID != f.ID || it.Dirty {
		t.Fatalf("Drive holds %q under %q, here the file is %q, dirty %v", f.Content, f.ID, it.ID, it.Dirty)
	}
}

func TestFileIsWrittenAndReadWhileItTakesItsFileID(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	ino, wh := createFile(t, env, "", "busy.bin")
	rh := openFile(t, env, "busy.bin")
	defer env.FS.Release(rh)
	var written atomic.Int64
	stop := make(chan struct{})
	failed := make(chan error, 1)
	go busyWriter(env, wh, rh, &written, stop, failed)
	waitUntil(t, func() bool { return written.Load() > 20 })
	id, err := env.FS.GiveID(ino)
	if err != nil || meta.IsLocalKey(id) {
		t.Fatalf("the file took the fileId %q: %v", id, err)
	}
	at := written.Load()
	waitUntil(t, func() bool { return written.Load() > at+20 })
	close(stop)
	if err := <-failed; err != nil {
		t.Fatal(err)
	}
	want := make([]byte, written.Load())
	for i := range want {
		want[i] = byte(i)
	}
	env.FS.Release(wh)
	if got := readAll(t, env, "busy.bin"); !bytes.Equal(got, want) {
		t.Fatalf("the file reads %d bytes that differ from the %d written", len(got), len(want))
	}
	uploadAll(t, env)
	if f := inDrive(t, env, "busy.bin"); f.ID != id || !bytes.Equal(f.Content, want) {
		t.Fatalf("Drive holds %d bytes under %q, want the %d written under %q", len(f.Content), f.ID, len(want), id)
	}
}

func drainIDs(t *testing.T, env *testenv.Env) {
	t.Helper()
	for {
		if _, err := env.FS.Store().TakeID(); err != nil {
			break
		}
	}
	env.Emu.SetOffline(true)
}

func TestItemsMadeWithoutFileIDsAreChangedAsAnyOther(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	doc := env.Emu.WriteFile(env.Emu.Root(), "doc.txt", []byte("old"))
	env.Start()
	drainIDs(t, env)
	writeFile(t, env, "", "a.txt", []byte("a"))
	writeFile(t, env, "", "gone.txt", []byte("gone"))
	writeFile(t, env, "", "doc.tmp", []byte("new"))
	for _, name := range []string{"a.txt", "gone.txt", "doc.tmp"} {
		if !meta.IsLocalKey(item(t, env, name).ID) {
			t.Fatalf("scenario not built: %s has a fileId of Drive", name)
		}
	}
	rename(t, env, "", "a.txt", "", "b.txt")
	if err := env.FS.Unlink(env.MustResolve("").Ino, "gone.txt"); err != nil {
		t.Fatal(err)
	}

	rename(t, env, "", "doc.tmp", "", "doc.txt")
	if it := item(t, env, "doc.txt"); it.ID != doc {
		t.Fatalf("the file saved over a file of Drive has the fileId %q, want Drive's %q", it.ID, doc)
	}
	env.Emu.SetOffline(false)
	uploadAll(t, env)
	requireDriveHoldsTheEdits(t, env, doc)
	requireNothingTemporaryWentToDrive(t, env)
}
