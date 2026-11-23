package core_test

import (
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func requireShortcutReadsAsLink(t *testing.T, env *testenv.Env) core.Attr {
	t.Helper()
	a := env.MustResolve("Links/report")
	if a.Mode != syscall.S_IFLNK|0o777 {
		t.Fatalf("the shortcut's mode is %o", a.Mode)
	}
	if got, err := env.FS.Readlink(a.Ino); err != nil || got != "../Docs/report.txt" {
		t.Fatalf("readlink = %q, %v", got, err)
	}
	if got, err := env.FS.Readlink(env.MustResolve("docs").Ino); err != nil || got != "Docs" {
		t.Fatalf("readlink of the folder shortcut = %q, %v", got, err)
	}
	return a
}

func requireOnlyTheShortcutChangedInDrive(t *testing.T, emu *emulator.Emulator, link, report, docs, docsLink string) {
	t.Helper()
	if f, _ := emu.Get(link); f.Name != "renamed" {
		t.Fatalf("the shortcut in Drive is %q", f.Name)
	}
	if f, _ := emu.Get(report); f.Name != "2025.txt" || f.Trashed {
		t.Fatalf("the target changed: %q trashed %v", f.Name, f.Trashed)
	}
	if f, _ := emu.Get(docs); f.Trashed {
		t.Fatal("removing the folder shortcut trashed the folder")
	}
	if f, _ := emu.Get(docsLink); !f.Trashed {
		t.Fatal("the removed shortcut is not in Drive's trash")
	}
}

func TestShortcutIsARelativeLink(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	emu := env.Emu
	docs := emu.Mkdir(emu.Root(), "Docs")
	links := emu.Mkdir(emu.Root(), "Links")
	archive := emu.Mkdir(emu.Root(), "Archive")
	report := emu.WriteFile(docs, "report.txt", []byte("r"))
	link := emu.Put(emulator.File{Name: "report", MimeType: emulator.MimeShortcut, Parents: []string{links},
		ShortcutTarget: report})
	docsLink := emu.Put(emulator.File{Name: "docs", MimeType: emulator.MimeShortcut, Parents: []string{emu.Root()},
		ShortcutTarget: docs})
	env.Start()

	a := requireShortcutReadsAsLink(t, env)

	emu.Update(report, func(f *emulator.File) { f.Parents = []string{archive}; f.Name = "2025.txt" })
	poll(t, env)
	if got, _ := env.FS.Readlink(a.Ino); got != "../Archive/2025.txt" {
		t.Fatalf("after the move readlink = %q", got)
	}

	rename(t, env, "Links", "report", "Links", "renamed")
	if err := env.FS.Unlink(env.MustResolve("").Ino, "docs"); err != nil {
		t.Fatal(err)
	}
	uploadAll(t, env)
	requireOnlyTheShortcutChangedInDrive(t, emu, link, report, docs, docsLink)
	if got := env.Names(""); slices.Contains(got, "docs") {
		t.Fatalf("root still lists the removed shortcut: %q", got)
	}
}

func TestShortcutsToHiddenTargetsAreNotShown(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	emu := env.Emu
	early := testenv.Epoch().Add(-time.Hour)
	shared := emu.Put(emulator.File{Name: "shared.pdf", MimeType: "application/pdf"})
	gone := emu.Put(emulator.File{Name: "gone.txt", MimeType: "text/plain", Parents: []string{emu.Root()}, Trashed: true})
	emu.Put(emulator.File{Name: "to shared", MimeType: emulator.MimeShortcut, Parents: []string{emu.Root()},
		ShortcutTarget: shared})
	emu.Put(emulator.File{Name: "x.pdf", MimeType: emulator.MimeShortcut, Parents: []string{emu.Root()},
		ShortcutTarget: gone, Created: early})
	emu.Put(emulator.File{Name: "x.pdf", MimeType: emulator.MimeShortcut, Parents: []string{emu.Root()},
		ShortcutTarget: "1NotInMyDriveAtAll", Created: early})
	emu.Put(emulator.File{Name: "x.pdf", MimeType: "application/pdf", Parents: []string{emu.Root()},
		Content: []byte("real")})
	live := emu.WriteFile(emu.Root(), "live.txt", nil)
	emu.Put(emulator.File{Name: "to live", MimeType: emulator.MimeShortcut, Parents: []string{emu.Root()},
		ShortcutTarget: live})
	env.Start()
	if got := env.Names(""); !slices.Equal(got, []string{"live.txt", "to live", "x.pdf"}) {
		t.Fatalf("root lists %q", got)
	}
	if got := readAll(t, env, "x.pdf"); string(got) != "real" {
		t.Fatalf("x.pdf reads %q", got)
	}
	_, err := env.Resolve("to shared")
	testenv.RequireErrno(t, err, syscall.ENOENT)

	emu.Update(live, func(f *emulator.File) { f.Trashed = true })
	poll(t, env)
	if got := env.Names(""); !slices.Equal(got, []string{"x.pdf"}) {
		t.Fatalf("after the target went to the trash the root lists %q", got)
	}
}

func TestShortcutHearsOfTheFoldersAboveItsTarget(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	emu := env.Emu
	docs := emu.Mkdir(emu.Root(), "Docs")
	inner := emu.Mkdir(docs, "inner")
	links := emu.Mkdir(emu.Root(), "Links")
	archive := emu.Mkdir(emu.Root(), "Archive")
	report := emu.WriteFile(inner, "report.txt", []byte("r"))
	emu.Put(emulator.File{Name: "report", MimeType: emulator.MimeShortcut, Parents: []string{links},
		ShortcutTarget: report, Created: testenv.Epoch().Add(-time.Hour)})
	emu.WriteFile(links, "report", []byte("a file of the same name"))
	env.Start()
	const numbered = "report (1)"
	if got := env.Names("Links"); !slices.Equal(got, []string{"report", numbered}) {
		t.Fatalf("Links lists %q", got)
	}
	link := env.MustResolve("Links/report")
	linksIno := env.MustResolve("Links").Ino

	rec := &recorder{}
	env.FS.SetNotifier(rec)
	emu.Update(docs, func(f *emulator.File) { f.Parents = []string{archive} })
	poll(t, env)
	if got, _ := env.FS.Readlink(link.Ino); got != "../Archive/Docs/inner/report.txt" {
		t.Fatalf("after the folder moved readlink = %q", got)
	}
	if !toldOf(rec, link.Ino) {
		t.Fatalf("the kernel keeps the old size of the link: told of %q", rec.inodes)
	}

	rec = &recorder{}
	env.FS.SetNotifier(rec)
	emu.Update(links, func(f *emulator.File) { f.Parents = []string{archive} })
	poll(t, env)
	if got, _ := env.FS.Readlink(link.Ino); got != "../Docs/inner/report.txt" {
		t.Fatalf("after the shortcut's folder moved readlink = %q", got)
	}
	if !toldOf(rec, link.Ino) {
		t.Fatalf("the kernel keeps the old size of the link: told of %q", rec.inodes)
	}

	rec = &recorder{}
	env.FS.SetNotifier(rec)
	emu.Update(docs, func(f *emulator.File) { f.Trashed = true })
	poll(t, env)
	if got := env.Names("Archive/Links"); !slices.Equal(got, []string{numbered}) {
		t.Fatalf("after the target's folder was trashed Links lists %q", got)
	}
	if !heard(rec, linksIno, "report", numbered) {
		t.Fatalf("the kernel keeps the old names: told of %q", rec.entries)
	}
}

func TestShortcutHearsOfLocalChangesOfItsTarget(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	emu := env.Emu
	docs := emu.Mkdir(emu.Root(), "Docs")
	emu.Mkdir(emu.Root(), "Archive")
	report := emu.WriteFile(docs, "report.txt", []byte("r"))
	emu.Put(emulator.File{Name: "report", MimeType: emulator.MimeShortcut, Parents: []string{emu.Root()},
		ShortcutTarget: report, Created: testenv.Epoch().Add(-time.Hour)})
	emu.WriteFile(emu.Root(), "report", []byte("a file of the same name"))
	env.Start()
	const numbered = "report (1)"
	link := env.MustResolve("report")
	root := env.MustResolve("").Ino
	rec := &recorder{}
	env.FS.SetNotifier(rec)

	rename(t, env, "", "Docs", "Archive", "Docs")
	if got, _ := env.FS.Readlink(link.Ino); got != "Archive/Docs/report.txt" {
		t.Fatalf("after the folder moved readlink = %q", got)
	}
	waitUntil(t, func() bool { return toldOf(rec, link.Ino) })

	if err := env.FS.Unlink(env.MustResolve("Archive/Docs").Ino, "report.txt"); err != nil {
		t.Fatal(err)
	}
	if got := env.Names(""); !slices.Equal(got, []string{"Archive", numbered}) {
		t.Fatalf("after the target was removed the root lists %q", got)
	}
	waitUntil(t, func() bool { return heard(rec, root, "report", numbered) })
}

func TestShortcutToItsOwnFolderIsNotShown(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	emu := env.Emu
	shared := emu.Mkdir(emu.Root(), "Shared")

	emu.Put(emulator.File{Name: "inner", MimeType: emulator.MimeShortcut, Parents: []string{shared},
		ShortcutTarget: shared})
	inner := emu.Mkdir(shared, "inner")
	other := emu.Mkdir(emu.Root(), "Other")
	emu.Put(emulator.File{Name: "self", MimeType: emulator.MimeShortcut, Parents: []string{shared},
		ShortcutTarget: shared})
	emu.Put(emulator.File{Name: "up", MimeType: emulator.MimeShortcut, Parents: []string{inner}, ShortcutTarget: shared})
	emu.Put(emulator.File{Name: "top", MimeType: emulator.MimeShortcut, Parents: []string{inner},
		ShortcutTarget: emu.Root()})
	emu.Put(emulator.File{Name: "aside", MimeType: emulator.MimeShortcut, Parents: []string{inner}, ShortcutTarget: other})
	down := emu.Put(emulator.File{Name: "down", MimeType: emulator.MimeShortcut, Parents: []string{shared},
		ShortcutTarget: inner})
	env.Start()
	if got := env.Names("Shared"); !slices.Equal(got, []string{"down", "inner"}) {
		t.Fatalf("Shared lists %q", got)
	}
	if got := env.Names("Shared/inner"); !slices.Equal(got, []string{"aside"}) {
		t.Fatalf("Shared/inner lists %q", got)
	}
	if got, err := env.FS.Readlink(env.MustResolve("Shared/down").Ino); err != nil || got != "inner" {
		t.Fatalf("the shortcut to the folder reads %q, %v", got, err)
	}

	emu.Update(down, func(f *emulator.File) { f.Parents = []string{inner} })
	poll(t, env)
	if got := env.Names("Shared/inner"); !slices.Equal(got, []string{"aside"}) {
		t.Fatalf("after the move Shared/inner lists %q", got)
	}
}

func TestShortcutWithALinkPastThePathLimit(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	emu := env.Emu
	deep := emu.Root()
	for i := range 5 {
		deep = emu.Mkdir(deep, strings.Repeat(string(rune('a'+i)), 1000))
	}
	far := emu.WriteFile(deep, "far.txt", []byte("far"))
	near := emu.WriteFile(emu.Root(), "near.txt", []byte("near"))
	emu.Put(emulator.File{Name: "to far", MimeType: emulator.MimeShortcut, Parents: []string{emu.Root()},
		ShortcutTarget: far})
	emu.Put(emulator.File{Name: "to near", MimeType: emulator.MimeShortcut, Parents: []string{emu.Root()},
		ShortcutTarget: near})
	env.Start()
	_, err := env.FS.Readlink(env.MustResolve("to far").Ino)
	testenv.RequireErrno(t, err, syscall.ENAMETOOLONG)
	if got, err := env.FS.Readlink(env.MustResolve("to near").Ino); err != nil || got != "near.txt" {
		t.Fatalf("the short link reads %q, %v", got, err)
	}
}
