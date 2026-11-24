//go:build e2e

package e2e

import (
	"os"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
)

func gone(t *testing.T, path, after string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("after %s stat of %s: %v, want ENOENT", after, path, err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err == nil {
		f.Close()
	}
	if !os.IsNotExist(err) {
		t.Errorf("after %s an open to write %s: %v, want ENOENT", after, path, err)
	}
}

func TestASecondNameGoesWithItsItem(t *testing.T) {
	e := startEmu(t)
	project := e.Put(emulator.File{Name: "Project", MimeType: emulator.MimeFolder, SharedWithMe: true})
	e.Put(emulator.File{Name: "plan.txt", Parents: []string{project}, Content: []byte("plan"), SharedWithMe: true})
	other := e.Put(emulator.File{Name: "other.txt", Parents: []string{project}, Content: []byte("o"),
		SharedWithMe: true})
	d := newDaemon(t, e)
	d.start()
	read := func(path, want string) {
		t.Helper()
		if got, err := os.ReadFile(d.path(path)); err != nil || string(got) != want {
			t.Fatalf("%s reads %q, %v; want %q", path, got, err, want)
		}
	}
	read("/Shared with me/plan.txt", "plan")
	if err := os.Rename(d.path("/Shared with me/Project/plan.txt"),
		d.path("/Shared with me/Project/plan-r.txt")); err != nil {
		t.Fatal(err)
	}
	gone(t, d.path("/Shared with me/plan.txt"), "the move")
	read("/Shared with me/plan-r.txt", "plan")
	if err := os.Remove(d.path("/Shared with me/Project/plan-r.txt")); err != nil {
		t.Fatal(err)
	}
	gone(t, d.path("/Shared with me/plan-r.txt"), "the removal")
	if err := os.WriteFile(d.path("/Shared with me/plan-r.txt"), []byte("lost?"), 0o644); err == nil {
		t.Error("a write by the second name of the removed file passed")
	}
	gone(t, d.path("/Shared with me/plan-r.txt"), "the write by the second name")
	read("/Shared with me/other.txt", "o")
	e.Update(other, func(f *emulator.File) { f.Trashed = true })
	waitFor(t, "the trash in Drive to reach the mount", 20*time.Second, func() bool {
		_, err := os.Stat(d.path("/Shared with me/Project/other.txt"))
		return os.IsNotExist(err)
	})
	gone(t, d.path("/Shared with me/other.txt"), "the trash in Drive")
}
