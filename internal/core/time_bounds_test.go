package core_test

import (
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestATimeAtTheBoundStaysInDriveThroughAChangeOfMetadata(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	far := time.Date(60056, 5, 28, 5, 36, 10, 0, time.UTC)
	root := env.Emu.Root()
	dir := env.Emu.Mkdir(root, "dir")
	for _, name := range []string{"renamed.txt", "moved.txt", "trashed.txt"} {
		env.Emu.Put(emulator.File{ID: "far" + name[:4] + "0000", Name: name, Parents: []string{root}, Modified: far,
			Content: []byte(name)})
	}
	env.Start()
	rename(t, env, "", "renamed.txt", "", "renamed2.txt")
	rename(t, env, "", "moved.txt", "dir", "moved.txt")
	if err := env.FS.Unlink(env.MustResolve("").Ino, "trashed.txt"); err != nil {
		t.Fatal(err)
	}
	uploadAll(t, env)
	for _, id := range []string{"farrena0000", "farmove0000", "fartras0000"} {
		f, ok := env.Emu.Get(id)
		if !ok {
			t.Fatalf("%s is gone from Drive", id)
		}
		if !f.Modified.Equal(far) {
			t.Errorf("%s has the time %v in Drive after the change, want %v", f.Name, f.Modified, far)
		}
	}
	renamed, _ := env.Emu.Get("farrena0000")
	moved, _ := env.Emu.Get("farmove0000")
	trashed, _ := env.Emu.Get("fartras0000")
	if renamed.Name != "renamed2.txt" || len(moved.Parents) != 1 || moved.Parents[0] != dir || !trashed.Trashed {
		t.Fatalf("scenario not built: the changes did not reach Drive: %q, %v, trashed %v", renamed.Name,
			moved.Parents, trashed.Trashed)
	}
}

func TestATimeAtTheLowerBoundStaysInDriveThroughARename(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	old := time.Date(1600, 1, 2, 3, 4, 5, 0, time.UTC)
	env.Emu.Put(emulator.File{ID: "old000000000", Name: "old.txt", Parents: []string{env.Emu.Root()}, Modified: old,
		Content: []byte("old")})
	env.Start()
	rename(t, env, "", "old.txt", "", "old2.txt")
	uploadAll(t, env)
	if f, ok := env.Emu.Get("old000000000"); !ok || f.Name != "old2.txt" || !f.Modified.Equal(old) {
		t.Fatalf("Drive holds %q of the time %v after the rename, want old2.txt of %v", f.Name, f.Modified, old)
	}
}

func TestATimeAtTheBoundGoesNotWithContent(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	far := time.Date(60056, 5, 28, 5, 36, 10, 0, time.UTC)
	root := env.Emu.Root()
	env.Emu.Put(emulator.File{ID: "far000000000", Name: "a.txt", Parents: []string{root}, Modified: far,
		Content: []byte("the content of a")})
	b := env.Emu.WriteFile(root, "b.txt", []byte("the content of b"))
	env.Start()
	readAll(t, env, "a.txt")

	atRest(t, env)
	rename(t, env, "", "a.txt", "", "b.txt")
	uploadAll(t, env)
	f, ok := env.Emu.ByPath("b.txt")
	if !ok || f.ID != b || string(f.Content) != "the content of a" {
		t.Fatalf("scenario not built: b.txt in Drive is %s holding %q, want %s taking the content of a.txt", f.ID,
			f.Content, b)
	}
	if f.Modified.Year() == 2262 {
		t.Fatalf("the content went up with the bound of the store for its time: %v", f.Modified)
	}

	if it := item(t, env, "b.txt"); it.MetaDirty || it.Dirty {
		t.Fatalf("after the upload the item waits to go: %+v", it.Item)
	}
	poll(t, env)
	if got := env.MustResolve("b.txt").Mtime; !got.Equal(f.Modified) {
		t.Fatalf("after the poll the mount shows the time %v, Drive holds %v", got, f.Modified)
	}
}

func TestARenameOfAFileNoOneMayChangeKeepsItsTimeToTheMillisecond(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	at := time.Date(2026, 11, 22, 10, 0, 0, 120456789, time.UTC)
	env.Emu.Put(emulator.File{ID: "fixed0000002", Name: "fixed.txt", Parents: []string{env.Emu.Root()}, Modified: at,
		Content: []byte("fixed"), CannotModify: true})
	env.Start()
	rename(t, env, "", "fixed.txt", "", "fixed2.txt")
	uploadAll(t, env)
	if f, ok := env.Emu.Get("fixed0000002"); !ok || f.Name != "fixed2.txt" {
		t.Fatalf("Drive holds %q after the rename, want fixed2.txt", f.Name)
	}
}

func TestARenameOfAFileNoOneMayChangeKeepsItsTime(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	at := time.Date(2026, 11, 22, 10, 0, 0, 120e6, time.UTC)
	env.Emu.Put(emulator.File{ID: "fixed0000000", Name: "fixed.txt", Parents: []string{env.Emu.Root()}, Modified: at,
		Content: []byte("fixed"), CannotModify: true})
	env.Start()
	rename(t, env, "", "fixed.txt", "", "fixed2.txt")
	uploadAll(t, env)
	if f, ok := env.Emu.Get("fixed0000000"); !ok || f.Name != "fixed2.txt" || !f.Modified.Equal(at) {
		t.Fatalf("Drive holds %q of the time %v, want fixed2.txt of %v", f.Name, f.Modified, at)
	}
}
