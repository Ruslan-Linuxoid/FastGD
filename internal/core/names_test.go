package core_test

import (
	"context"
	"fmt"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func dups(env *testenv.Env) []string {
	ids := make([]string, 0, 3)
	for i, c := range []string{"oldest", "middle", "newest"} {
		ids = append(ids, env.Emu.Put(emulator.File{
			Name: "IMG.jpg", MimeType: "image/jpeg", Parents: []string{env.Emu.Root()},
			Content: []byte(c), Created: testenv.Epoch().Add(time.Duration(i-3) * time.Hour),
		}))
	}
	return ids
}

func dupName(n int) string { return fmt.Sprintf("IMG (%d).jpg", n) }

func TestEachDuplicateIsItsOwnFile(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	ids := dups(env)
	env.Start()
	for i, name := range []string{"IMG.jpg", dupName(1), dupName(2)} {
		want := []string{"oldest", "middle", "newest"}[i]
		if got := readAll(t, env, name); string(got) != want {
			t.Fatalf("%s reads %q, want %q", name, got, want)
		}
	}

	if err := env.FS.Truncate(context.Background(), env.MustResolve(dupName(2)).Ino, 3); err != nil {
		t.Fatal(err)
	}

	rename(t, env, "", dupName(1), "", "middle.jpg")
	uploadAll(t, env)
	if got := env.Names(""); !slices.Equal(got, []string{dupName(2), "IMG.jpg", "middle.jpg"}) {
		t.Fatalf("after the rename of a duplicate the root lists %q", got)
	}
	if f, _ := env.Emu.Get(ids[1]); f.Name != "middle.jpg" || string(f.Content) != "middle" {
		t.Fatalf("the renamed middle duplicate is %q with %q in Drive", f.Name, f.Content)
	}
	for id, want := range map[string]string{ids[0]: "oldest", ids[2]: "new"} {
		if f, _ := env.Emu.Get(id); f.Name != "IMG.jpg" || string(f.Content) != want {
			t.Fatalf("a duplicate is %q with %q in Drive, want IMG.jpg with %q", f.Name, f.Content, want)
		}
	}
}

func TestDuplicatesKeepTheirNumbersWhenOneLeaves(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	ids := dups(env)
	env.Start()
	rec := &recorder{}
	env.FS.SetNotifier(rec)
	root := env.MustResolve("").Ino

	if err := env.FS.Unlink(root, "IMG.jpg"); err != nil {
		t.Fatal(err)
	}
	if got := env.Names(""); !slices.Equal(got, []string{dupName(1), dupName(2)}) {
		t.Fatalf("after removing the oldest the root lists %q", got)
	}
	if got := readAll(t, env, dupName(1)); string(got) != "middle" {
		t.Fatalf("%s reads %q, want the middle one", dupName(1), got)
	}
	_, err := env.Resolve("IMG.jpg")
	testenv.RequireErrno(t, err, syscall.ENOENT)

	waitUntil(t, func() bool { return heard(rec, root, "IMG.jpg") })

	env.Emu.Update(ids[1], func(f *emulator.File) { f.Trashed = true })
	poll(t, env)
	if got := env.Names(""); !slices.Equal(got, []string{dupName(2)}) {
		t.Fatalf("after Drive trashed the middle one the root lists %q", got)
	}
	uploadAll(t, env)
	env.Restart()
	if got := env.Names(""); !slices.Equal(got, []string{dupName(2)}) {
		t.Fatalf("after a restart the root lists %q", got)
	}
	if got := readAll(t, env, dupName(2)); string(got) != "newest" {
		t.Fatalf("%s reads %q, want the newest one", dupName(2), got)
	}

	for _, c := range []string{"fourth", "fifth"} {
		env.Emu.WriteFile(env.Emu.Root(), "IMG.jpg", []byte(c))
		poll(t, env)
	}
	for name, want := range map[string]string{"IMG.jpg": "fourth", dupName(1): "fifth", dupName(2): "newest"} {
		if got := readAll(t, env, name); string(got) != want {
			t.Fatalf("%s reads %q, want %q", name, got, want)
		}
	}
}

func TestNumberOfADuplicateStaysOutOfDrive(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	emu := env.Emu
	put := func(name, content string, age time.Duration) string {
		return emu.Put(emulator.File{
			Name: name, MimeType: "text/plain", Parents: []string{emu.Root()},
			Content: []byte(content), Created: testenv.Epoch().Add(-age),
		})
	}
	put("a.txt", "first", 3*time.Hour)
	dup := put("a.txt", "duplicate", 2*time.Hour)
	realFile := put("a (1).txt", "real", time.Hour)
	env.Start()
	for name, want := range map[string]string{"a.txt": "first", "a (1).txt": "real", "a (2).txt": "duplicate"} {
		if got := readAll(t, env, name); string(got) != want {
			t.Fatalf("%s reads %q, want %q; the root lists %q", name, got, want, env.Names(""))
		}
	}

	mkdir(t, env, "", "sub")
	rename(t, env, "", "a (2).txt", "sub", "a (2).txt")
	if got := env.Names("sub"); !slices.Equal(got, []string{"a.txt"}) {
		t.Fatalf("the moved duplicate shows as %q in a folder where its name is free", got)
	}
	rename(t, env, "", "a (1).txt", "sub", "a (1).txt")
	uploadAll(t, env)
	if f, _ := emu.Get(dup); f.Name != "a.txt" {
		t.Fatalf("the moved duplicate is %q in Drive, want its own name", f.Name)
	}
	if f, _ := emu.Get(realFile); f.Name != "a (1).txt" {
		t.Fatalf("the file of a numbered name is %q in Drive after its move", f.Name)
	}
	if got := env.Names("sub"); !slices.Equal(got, []string{"a (1).txt", "a.txt"}) {
		t.Fatalf("the folder of the two moved lists %q", got)
	}
}

func TestSlashInNamesGoesToDriveAsSlash(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	writeFile(t, env, "", "a／b.txt", []byte("x"))
	mkdir(t, env, "", "1／2")
	uploadAll(t, env)
	inDrive(t, env, "a/b.txt")
	inDrive(t, env, "1/2")
	rename(t, env, "", "a／b.txt", "", "c／d.txt")
	uploadAll(t, env)
	inDrive(t, env, "c/d.txt")
	if got := env.Names(""); !slices.Equal(got, []string{"1／2", "c／d.txt"}) {
		t.Fatalf("root lists %q", got)
	}
}

func TestRenamingTheEarliestDuplicateTellsTheKernel(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	dups(env)
	env.Start()
	rec := &recorder{}
	env.FS.SetNotifier(rec)
	root := env.MustResolve("").Ino
	rename(t, env, "", "IMG.jpg", "", "first.jpg")
	if got := readAll(t, env, dupName(1)); string(got) != "middle" {
		t.Fatalf("%s reads %q after the rename", dupName(1), got)
	}
	_, err := env.Resolve("IMG.jpg")
	testenv.RequireErrno(t, err, syscall.ENOENT)
	waitUntil(t, func() bool { return heard(rec, root, "IMG.jpg", "first.jpg") })
}

func TestGroupsOfOneDriveNameAreToldApart(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	emu := env.Emu
	doc := emu.Put(emulator.File{Name: "R", MimeType: "application/vnd.google-apps.document",
		Parents: []string{emu.Root()}})
	file := emu.WriteFile(emu.Root(), "R", []byte("x"))
	env.Start()
	if got := env.Names(""); !slices.Equal(got, []string{"R", "R.gdoc"}) {
		t.Fatalf("root lists %q", got)
	}
	root := env.MustResolve("").Ino
	rec := &recorder{}
	env.FS.SetNotifier(rec)
	emu.Update(doc, func(f *emulator.File) { f.Trashed = true })
	emu.Update(file, func(f *emulator.File) { f.Trashed = true })
	poll(t, env)
	if !heard(rec, root, "R", "R.gdoc") {
		t.Fatalf("the kernel was told of %q", rec.entries)
	}
}

func TestNamesThatAreNotUTF8AreRefused(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	root := env.MustResolve("").Ino
	writeFile(t, env, "", "файл.txt", []byte("x"))
	uploadAll(t, env)
	env.Emu.ResetRequests()
	for _, bad := range []string{"\xff.txt", "половина \xd0", "a\xc3\x28b"} {
		_, _, err := env.FS.Create(root, bad, syscall.O_WRONLY)
		testenv.RequireErrno(t, err, syscall.EILSEQ)
		_, err = env.FS.Mkdir(root, bad)
		testenv.RequireErrno(t, err, syscall.EILSEQ)
		testenv.RequireErrno(t, env.FS.Rename(root, "файл.txt", root, bad, 0), syscall.EILSEQ)
	}
	if got := env.Names(""); !slices.Equal(got, []string{"файл.txt"}) {
		t.Fatalf("after the refused names the root lists %q", got)
	}
	uploadAll(t, env)
	if content, metadata := uploads(env); content+metadata != 0 {
		t.Fatalf("the refused names sent %d requests to Drive", content+metadata)
	}
	mkdir(t, env, "", "папка／с косой")
	rename(t, env, "", "файл.txt", "папка／с косой", "ещё один.txt")
	uploadAll(t, env)
	inDrive(t, env, "папка/с косой", "ещё один.txt")
}
