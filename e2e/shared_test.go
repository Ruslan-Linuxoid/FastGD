//go:build e2e

package e2e

import (
	"errors"
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
)

func holds(e *emu, parent, name, want string) bool {
	for _, f := range e.Children(parent) {
		if f.Name == name && string(f.Content) == want {
			return true
		}
	}
	return false
}

func TestTheRootOfTheMount(t *testing.T) {
	e := startEmu(t)
	d := newDaemon(t, e)
	d.start()
	if got := dirNames(t, d.mnt); !slices.Equal(got, []string{"My Drive", "Shared with me"}) {
		t.Fatalf("with no shared drive the root lists %q", got)
	}
	e.AddSharedDrive("Team")
	waitFor(t, "Shared drives with the drive that came", 20*time.Second, func() bool {
		return slices.Equal(dirNames(t, d.mnt), []string{"My Drive", "Shared drives", "Shared with me"}) &&
			slices.Equal(dirNames(t, d.path("/Shared drives")), []string{"Team"})
	})
	for _, p := range []string{"/new", "/Shared with me/new", "/Shared drives/new"} {
		if err := os.Mkdir(d.path(p), 0o755); !errors.Is(err, syscall.EPERM) {
			t.Errorf("mkdir %s: %v, want EPERM", p, err)
		}
	}
	if err := os.WriteFile(d.path("/new.txt"), []byte("x"), 0o644); !errors.Is(err, syscall.EPERM) {
		t.Errorf("a file made in the root: %v, want EPERM", err)
	}
	for _, p := range []string{"/My Drive", "/Shared drives/Team", "/Shared with me"} {
		if err := os.Remove(d.path(p)); !errors.Is(err, syscall.EPERM) {
			t.Errorf("rmdir %s: %v, want EPERM", p, err)
		}
		if err := os.Rename(d.path(p), d.path(p+" 2")); !errors.Is(err, syscall.EPERM) {
			t.Errorf("rename %s: %v, want EPERM", p, err)
		}
	}
}

func TestASharedDriveReadsAndWrites(t *testing.T) {
	e := startEmu(t)
	team := e.AddSharedDrive("Team")
	e.WriteFile(team, "a.txt", []byte("of the team"))
	locked := e.WriteFile(team, "locked.txt", []byte("l"))
	e.Update(locked, func(f *emulator.File) { f.CannotModify = true })
	d := newDaemon(t, e)
	d.start()
	if got, err := os.ReadFile(d.path("/Shared drives/Team/a.txt")); err != nil || string(got) != "of the team" {
		t.Fatalf("a file of the shared drive reads %q, %v", got, err)
	}
	if err := os.WriteFile(d.path("/Shared drives/Team/b.txt"), []byte("mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the file written to reach the shared drive", 20*time.Second, func() bool {
		return holds(e, team, "b.txt", "mine")
	})
	if _, err := os.OpenFile(d.path("/Shared drives/Team/locked.txt"), os.O_WRONLY, 0); !errors.Is(err,
		syscall.EACCES) {
		t.Fatalf("a write of a file without the right to change it: %v, want EACCES", err)
	}
}

func TestMovesAcrossPartsOfTheMount(t *testing.T) {
	e := startEmu(t)
	team := e.AddSharedDrive("Team")
	fixed := e.WriteFile(team, "fixed.txt", []byte("f"))
	e.Update(fixed, func(f *emulator.File) { f.CannotMoveOutOfDrive = true })
	e.WriteFile(team, "free.txt", []byte("free"))
	plans := e.Put(emulator.File{Name: "Plans", MimeType: emulator.MimeFolder, SharedWithMe: true})
	e.Put(emulator.File{Name: "c.txt", Parents: []string{plans}, Content: []byte("c"), SharedWithMe: true})
	d := newDaemon(t, e)
	d.start()
	for from, to := range map[string]string{"/Shared drives/Team/fixed.txt": "fixed.txt",
		"/Shared with me/Plans/c.txt": "c.txt", "a.txt": "/Shared with me/Plans/a.txt"} {
		if from == "a.txt" {
			if err := os.WriteFile(d.path(from), []byte("a"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Rename(d.path(from), d.path(to)); !errors.Is(err, syscall.EXDEV) {
			t.Errorf("move %s to %s: %v, want EXDEV", from, to, err)
		}
	}
	if err := os.Rename(d.path("/Shared drives/Team/free.txt"), d.path("free.txt")); err != nil {
		t.Fatalf("a move out of the shared drive by an item that may leave it: %v", err)
	}
	waitFor(t, "the moved file in My Drive", 20*time.Second, func() bool {
		return holds(e, e.Root(), "free.txt", "free")
	})
}

func TestAReaderOfASharedDriveMakesNothingInIt(t *testing.T) {
	e := startEmu(t)
	team := e.AddSharedDrive("Team")
	e.UpdateSharedDrive(team, func(f *emulator.File) { f.CannotAddChildren = true })
	d := newDaemon(t, e)
	d.start()
	if err := os.Mkdir(d.path("/Shared drives/Team/notes"), 0o755); !errors.Is(err, syscall.EACCES) {
		t.Errorf("mkdir in the root of the drive of a reader: %v, want EACCES", err)
	}
	if err := os.WriteFile(d.path("/Shared drives/Team/n.txt"), []byte("n"), 0o644); !errors.Is(err,
		syscall.EACCES) {
		t.Errorf("a create in the root of the drive of a reader: %v, want EACCES", err)
	}
	if _, err := os.Stat(d.path("/Lost and Found (FastGD)")); !os.IsNotExist(err) {
		t.Errorf("Lost and Found is there: %v", err)
	}
	if n := strings.Count(d.out.String(), "Drive does not let the user add an item to the folder"); n != 1 {
		t.Errorf("the log tells the refusals %d times, want once:\n%s", n, d.out)
	}
}
