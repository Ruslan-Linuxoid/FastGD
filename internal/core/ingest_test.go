package core_test

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestInitialLoadShowsOnlyMyDrive(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	emu := env.Emu
	root := emu.Root()
	docs := emu.Mkdir(root, "Docs")
	emu.WriteFile(docs, "a.txt", []byte("a"))
	old := emu.Put(emulator.File{Name: "Old", MimeType: emulator.MimeFolder, Parents: []string{root}, Trashed: true})
	emu.WriteFile(old, "inner.txt", []byte("i"))
	emu.Put(emulator.File{Name: "gone.txt", MimeType: "text/plain", Parents: []string{root}, Trashed: true})

	emu.Put(emulator.File{Name: "shared.pdf", MimeType: "application/pdf"})
	emu.Put(emulator.File{Name: "deep.pdf", MimeType: "application/pdf", Parents: []string{"1foreignFolderNotInMyDrive"}})
	env.Start()

	if got, want := env.Names(""), []string{"Docs"}; !slices.Equal(got, want) {
		t.Fatalf("root lists %q, want %q", got, want)
	}
	if got := env.Names("Docs"); !slices.Equal(got, []string{"a.txt"}) {
		t.Fatalf("Docs lists %q", got)
	}
	for _, p := range []string{"Old", "Old/inner.txt", "gone.txt", "shared.pdf", "deep.pdf"} {
		_, err := env.Resolve(p)
		testenv.RequireErrno(t, err, syscall.ENOENT)
	}
}

func TestSimilarFolderNamesStayApart(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	emu := env.Emu
	root := emu.Root()
	for _, name := range []string{"a_b", "aXb", "Photos", "photos", "a%b"} {
		dir := emu.Mkdir(root, name)
		emu.WriteFile(dir, "in "+name, nil)
	}
	env.Start()

	got := env.Names("")
	slices.Sort(got)
	if want := []string{"Photos", "a%b", "aXb", "a_b", "photos"}; !slices.Equal(got, want) {
		t.Fatalf("root lists %q, want %q", got, want)
	}
	for _, name := range []string{"a_b", "aXb", "Photos", "photos", "a%b"} {
		if got := env.Names(name); !slices.Equal(got, []string{"in " + name}) {
			t.Errorf("%s lists %q", name, got)
		}
	}
	_, err := env.Resolve("PHOTOS")
	testenv.RequireErrno(t, err, syscall.ENOENT)
}

func TestAttributesComeFromDrive(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	emu := env.Emu
	mtime := time.Date(2025, 6, 7, 8, 9, 10, 123e6, time.UTC)
	dir := emu.Put(emulator.File{Name: "dir", MimeType: emulator.MimeFolder, Parents: []string{emu.Root()},
		Modified: mtime})
	emu.Put(emulator.File{Name: "f.bin", MimeType: "application/octet-stream", Parents: []string{dir},
		Content: make([]byte, 12345), Modified: mtime})
	env.Start()

	f := env.MustResolve("dir/f.bin")
	if f.Mode != syscall.S_IFREG|0o644 || f.Size != 12345 || !f.Mtime.Equal(mtime) || f.UID != testenv.UID ||
		f.GID != testenv.GID {
		t.Fatalf("file attr %+v", f)
	}
	d := env.MustResolve("dir")
	if d.Mode != syscall.S_IFDIR|0o755 || !d.Mtime.Equal(mtime) || d.UID != testenv.UID || d.GID != testenv.GID {
		t.Fatalf("dir attr %+v", d)
	}
}

func TestLookupInsideAFileIsENOTDIR(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Emu.WriteFile(env.Emu.Root(), "f", []byte("x"))
	env.Start()
	_, err := env.FS.Lookup(env.MustResolve("f").Ino, "x")
	testenv.RequireErrno(t, err, syscall.ENOTDIR)
}

func TestInodeNumbersStayAcrossARestart(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	dir := env.Emu.Mkdir(env.Emu.Root(), "dir")
	old := env.Emu.WriteFile(dir, "f", []byte("x"))
	env.Start()
	was := map[string]uint64{}
	for _, p := range []string{"dir", "dir/f"} {
		was[p] = env.MustResolve(p).Ino
	}
	env.Restart()
	for p, ino := range was {
		if now := env.MustResolve(p).Ino; now != ino {
			t.Fatalf("after a restart %s has the inode %d, want the %d it had", p, now, ino)
		}
	}
	env.Emu.Delete(old)
	env.Emu.WriteFile(dir, "f", []byte("another"))
	poll(t, env)
	if now := env.MustResolve("dir/f").Ino; now == was["dir/f"] {
		t.Fatalf("another file of the name got the inode %d of the one that is gone", now)
	}
}

func TestLoadCutShortIsDoneAnew(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)

	const files = 1500
	for i := range files {
		env.Emu.WriteFile(env.Emu.Root(), fmt.Sprintf("f%04d", i), nil)
	}
	env.Emu.OnListPage(func(n int) {
		if n == 1 {
			env.Emu.SetOffline(true)
		}
	})
	if _, err := env.Open(); err == nil {
		t.Fatal("a start whose load of the tree was cut short went through")
	}
	env.Emu.OnListPage(nil)
	env.Emu.SetOffline(false)
	env.Start()
	if got := len(env.Names("")); got != files {
		t.Fatalf("after the load was done anew the root lists %d files, want %d", got, files)
	}
}

func TestInitialLoadReadsAllPages(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	dir := env.Emu.Mkdir(env.Emu.Root(), "big")
	for i := range 2500 {
		env.Emu.WriteFile(dir, fmt.Sprintf("f%04d", i), nil)
	}
	env.Start()

	if got := len(env.Names("big")); got != 2500 {
		t.Fatalf("big lists %d files, want 2500", got)
	}
	pages := 0
	for _, r := range env.Emu.Requests() {
		if r.Path == "/drive/v3/files" {
			pages++
			if r.Query.Get("pageSize") != "1000" {
				t.Errorf("list page size %q, want 1000", r.Query.Get("pageSize"))
			}
		}
	}
	if pages != 3 {
		t.Fatalf("%d list pages, want 3", pages)
	}
}

func TestRestartServesFromDatabase(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	dir := env.Emu.Mkdir(env.Emu.Root(), "dir")
	env.Emu.WriteFile(dir, "f", []byte("x"))
	env.Start()
	env.Emu.ResetRequests()

	env.Restart()
	if n := len(env.Emu.Requests()); n != 0 {
		t.Fatalf("restart made %d requests to Drive", n)
	}
	env.Srv.Close()
	if got := env.Names("dir"); !slices.Equal(got, []string{"f"}) {
		t.Fatalf("with Drive down dir lists %q", got)
	}

	env.Restart()
	if got := env.Names("dir"); !slices.Equal(got, []string{"f"}) {
		t.Fatalf("after a start with Drive down dir lists %q", got)
	}
	if a := env.MustResolve("dir/f"); a.Size != 1 {
		t.Fatalf("with Drive down dir/f is %+v", a)
	}
}

func TestDuplicateAndSlashNames(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	emu := env.Emu
	root := emu.Root()
	older := emu.Put(emulator.File{Name: "IMG.jpg", MimeType: "image/jpeg", Parents: []string{root}, Content: []byte("1"),
		Created: testenv.Epoch().Add(-2 * time.Hour)})
	emu.Put(emulator.File{Name: "IMG.jpg", MimeType: "image/jpeg", Parents: []string{root}, Content: []byte("22"),
		Created: testenv.Epoch().Add(-time.Hour)})
	emu.WriteFile(root, "a/b.txt", []byte("333"))
	env.Start()

	const dup = "IMG (1).jpg"
	got := env.Names("")
	slices.Sort(got)
	if want := []string{dup, "IMG.jpg", "a／b.txt"}; !slices.Equal(got, want) {
		t.Fatalf("root lists %q, want %q", got, want)
	}
	if a := env.MustResolve("IMG.jpg"); a.Size != 1 {
		t.Fatalf("IMG.jpg is not the older file %s: %+v", older, a)
	}
	if a := env.MustResolve(dup); a.Size != 2 {
		t.Fatalf("%s is not the newer file: %+v", dup, a)
	}
	if a := env.MustResolve("a／b.txt"); a.Size != 3 {
		t.Fatalf("a／b.txt: %+v", a)
	}
	_, err := env.Resolve("IMG [" + older[:8] + "].jpg")
	testenv.RequireErrno(t, err, syscall.ENOENT)
}

func TestLoadedItemsAreFoundByIDAndPath(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	emu := env.Emu
	a := emu.Mkdir(emu.Root(), "a")
	b := emu.Mkdir(a, "b")
	c := emu.WriteFile(b, "c.txt", []byte("hello"))

	shared := emu.Put(emulator.File{Name: "Team", MimeType: emulator.MimeFolder})
	inShared := emu.WriteFile(shared, "plan.txt", []byte("p"))
	env.Start()
	st := env.FS.Store()

	it, err := st.ByID(c)
	if err != nil {
		t.Fatal(err)
	}
	sum := md5.Sum([]byte("hello"))
	if it.MD5 != hex.EncodeToString(sum[:]) || it.Version != 1 || it.Size != 5 {
		t.Fatalf("c.txt in the database: %+v", it)
	}
	if p, err := st.Path(c); err != nil || p != "a/b/c.txt" {
		t.Fatalf("Path(c.txt) = %q, %v", p, err)
	}
	for _, id := range []string{shared, inShared} {
		if p, err := st.Path(id); !errors.Is(err, meta.ErrNotShown) {
			t.Errorf("Path(%s) = %q, %v; want ErrNotShown", id, p, err)
		}
	}
	if got := env.Names(""); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("root lists %q", got)
	}
}

func TestSeveralParentsUseTheFirst(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	emu := env.Emu
	first := emu.Mkdir(emu.Root(), "first")
	second := emu.Mkdir(emu.Root(), "second")
	emu.Put(emulator.File{Name: "f", MimeType: "text/plain", Parents: []string{first, second}})
	env.Start()

	if got := env.Names("first"); !slices.Equal(got, []string{"f"}) {
		t.Fatalf("first lists %q", got)
	}
	if got := env.Names("second"); len(got) != 0 {
		t.Fatalf("second lists %q", got)
	}
}
