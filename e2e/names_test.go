//go:build e2e

package e2e

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
)

func TestShortcutReadsAtAnotherPath(t *testing.T) {
	e := startEmu(t)
	docs := e.Mkdir(e.Root(), "Docs")
	links := e.Mkdir(e.Root(), "Links")
	report := e.WriteFile(docs, "report.txt", []byte("the report"))
	e.Put(emulator.File{Name: "report", MimeType: emulator.MimeShortcut, Parents: []string{links}, ShortcutTarget: report})
	d := newDaemon(t, e)
	d.start()

	if got, err := os.Readlink(d.path("Links/report")); err != nil || got != "../Docs/report.txt" {
		t.Fatalf("readlink: %q, %v", got, err)
	}

	other := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mount(d.mnt, other, "", syscall.MS_BIND, ""); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Unmount(other, syscall.MNT_DETACH) })
	if got, err := os.ReadFile(filepath.Join(other, "My Drive/Links/report")); err != nil || string(got) != "the report" {
		t.Fatalf("reading through the shortcut at another path: %q, %v", got, err)
	}
}

func TestSameNamesThroughTheKernel(t *testing.T) {
	e := startEmu(t)
	docs := e.Mkdir(e.Root(), "Docs")
	e.Put(emulator.File{Name: "IMG.jpg", MimeType: "image/jpeg", Parents: []string{docs}, Content: []byte("1"),
		Created: time.Now().Add(-time.Hour)})
	e.Put(emulator.File{Name: "IMG.jpg", MimeType: "image/jpeg", Parents: []string{docs}, Content: []byte("2")})
	d := newDaemon(t, e)
	d.start()

	const dup = "IMG (1).jpg"

	if got := dirNames(t, d.path("Docs")); !slices.Equal(got, []string{dup, "IMG.jpg"}) {
		t.Fatalf("Docs lists %q", got)
	}
	for name, want := range map[string]string{"IMG.jpg": "1", dup: "2"} {
		if got, err := os.ReadFile(d.path("Docs/" + name)); err != nil || string(got) != want {
			t.Fatalf("%s reads %q, %v", name, got, err)
		}
	}
}

func TestShortcutFollowsItsTargetThroughTheKernel(t *testing.T) {
	e := startEmu(t)
	docs := e.Mkdir(e.Root(), "Docs")
	archive := e.Mkdir(e.Root(), "Archive")
	report := e.WriteFile(docs, "report.txt", []byte("r"))
	e.Put(emulator.File{Name: "report", MimeType: emulator.MimeShortcut, Parents: []string{e.Root()},
		ShortcutTarget: report})
	d := newDaemon(t, e)
	d.start()

	if _, err := os.Lstat(d.path("report")); err != nil {
		t.Fatal(err)
	}
	e.Update(report, func(f *emulator.File) { f.Parents = []string{archive} })
	waitFor(t, "the link to follow the move", 20*time.Second, func() bool {
		got, err := os.Readlink(d.path("report"))
		fi, lerr := os.Lstat(d.path("report"))
		return err == nil && got == "Archive/report.txt" && lerr == nil && fi.Size() == int64(len(got))
	})
	e.Update(report, func(f *emulator.File) { f.Trashed = true })
	waitFor(t, "the shortcut to go with its target", 20*time.Second, func() bool {
		_, err := os.Lstat(d.path("report"))
		return os.IsNotExist(err)
	})
}

func TestShortcutFollowsTheFolderAboveItsTarget(t *testing.T) {
	e := startEmu(t)
	docs := e.Mkdir(e.Root(), "Docs")
	archive := e.Mkdir(e.Root(), "Archive")
	report := e.WriteFile(docs, "report.txt", []byte("r"))
	e.Put(emulator.File{Name: "report", MimeType: emulator.MimeShortcut, Parents: []string{e.Root()},
		ShortcutTarget: report, Created: time.Now().Add(-time.Hour)})
	e.WriteFile(e.Root(), "report", []byte("a file of the same name"))
	d := newDaemon(t, e)
	d.start()

	if fi, err := os.Lstat(d.path("report")); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("lstat of the shortcut: %v, %v", fi, err)
	}
	e.Update(docs, func(f *emulator.File) { f.Parents = []string{archive} })
	waitFor(t, "the link to follow the folder", 20*time.Second, func() bool {
		got, err := os.Readlink(d.path("report"))
		fi, lerr := os.Lstat(d.path("report"))
		return err == nil && got == "Archive/Docs/report.txt" && lerr == nil && fi.Size() == int64(len(got))
	})
	e.Update(archive, func(f *emulator.File) { f.Trashed = true })

	waitFor(t, "the shortcut to go with the folder of its target", 20*time.Second, func() bool {
		_, err := os.Lstat(d.path("report"))
		return os.IsNotExist(err)
	})
	if got, err := os.ReadFile(d.path("report (1)")); err != nil || string(got) != "a file of the same name" {
		t.Fatalf("the file of the shortcut's name reads %q, %v", got, err)
	}
	if got := dirNames(t, d.path("")); !slices.Equal(got, []string{"report (1)"}) {
		t.Fatalf("the root lists %q", got)
	}
}

func TestShortcutFollowsALocalMoveAboveItsTarget(t *testing.T) {
	e := startEmu(t)
	docs := e.Mkdir(e.Root(), "Docs")
	e.Mkdir(e.Root(), "Archive")
	report := e.WriteFile(docs, "report.txt", []byte("r"))
	e.Put(emulator.File{Name: "report", MimeType: emulator.MimeShortcut, Parents: []string{e.Root()},
		ShortcutTarget: report})
	d := newDaemon(t, e)
	d.start()
	if _, err := os.Lstat(d.path("report")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(d.path("Docs"), d.path("Archive/Docs")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the size of the link to follow the move", 5*time.Second, func() bool {
		got, err := os.Readlink(d.path("report"))
		fi, lerr := os.Lstat(d.path("report"))
		return err == nil && got == "Archive/Docs/report.txt" && lerr == nil && fi.Size() == int64(len(got))
	})
}

func TestNamesThatAreNotUTF8ThroughTheKernel(t *testing.T) {
	e := startEmu(t)
	e.WriteFile(e.Root(), "plain.txt", []byte("x"))
	d := newDaemon(t, e)
	d.start()
	bad := d.path("\xff.txt")
	if err := os.WriteFile(bad, []byte("x"), 0o644); !errors.Is(err, syscall.EILSEQ) {
		t.Fatalf("a file under a name that is not UTF-8: %v, want EILSEQ", err)
	}
	if err := os.Mkdir(bad, 0o755); !errors.Is(err, syscall.EILSEQ) {
		t.Fatalf("a folder under a name that is not UTF-8: %v, want EILSEQ", err)
	}
	if err := os.Rename(d.path("plain.txt"), bad); !errors.Is(err, syscall.EILSEQ) {
		t.Fatalf("a rename to a name that is not UTF-8: %v, want EILSEQ", err)
	}
	if got := dirNames(t, d.path("")); !slices.Equal(got, []string{"plain.txt"}) {
		t.Fatalf("the root lists %q", got)
	}
}
