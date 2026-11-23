//go:build e2e

package e2e

import (
	"errors"
	"os"
	"syscall"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
)

func TestForeignFileRefusesChanges(t *testing.T) {
	e := startEmu(t)
	e.Put(emulator.File{Name: "theirs.txt", MimeType: "text/plain", Parents: []string{e.Root()},
		Content: []byte("theirs"), CannotModify: true, CannotTrash: true, CannotRename: true})
	d := newDaemon(t, e)
	d.start()

	fi, err := os.Stat(d.path("theirs.txt"))
	if err != nil || fi.Mode().Perm() != 0o444 {
		t.Fatalf("stat: %v, %v", fi, err)
	}
	if got, err := os.ReadFile(d.path("theirs.txt")); err != nil || string(got) != "theirs" {
		t.Fatalf("read: %q, %v", got, err)
	}
	if err := os.WriteFile(d.path("theirs.txt"), []byte("mine"), 0o644); !errors.Is(err, syscall.EACCES) {
		t.Fatalf("a write: %v, want EACCES", err)
	}
	if err := os.Remove(d.path("theirs.txt")); !errors.Is(err, syscall.EACCES) {
		t.Fatalf("rm: %v, want EACCES", err)
	}
	if err := os.Rename(d.path("theirs.txt"), d.path("renamed.txt")); !errors.Is(err, syscall.EACCES) {
		t.Fatalf("mv: %v, want EACCES", err)
	}
	if _, err := os.Stat(d.path("theirs.txt")); err != nil {
		t.Fatalf("the file is gone from the mount: %v", err)
	}
}
