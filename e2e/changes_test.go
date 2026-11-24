//go:build e2e

package e2e

import (
	"os"
	"slices"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
)

func waitFor(t *testing.T, what string, within time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(within)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: not within %v", what, within)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestChangesInDriveReachTheMount(t *testing.T) {
	e := startEmu(t)
	docs := e.Mkdir(e.Root(), "Docs")
	edited := e.WriteFile(docs, "edited.txt", []byte("old"))
	renamed := e.WriteFile(docs, "old-name.txt", []byte("r"))
	trashed := e.WriteFile(docs, "trashed.txt", []byte("t"))
	restored := e.Put(emulator.File{Name: "restored.txt", MimeType: "text/plain", Parents: []string{docs}, Trashed: true})
	d := newDaemon(t, e)
	d.start()

	if got, err := os.ReadFile(d.path("Docs/edited.txt")); err != nil || string(got) != "old" {
		t.Fatalf("edited.txt before: %q, %v", got, err)
	}
	dirNames(t, d.path("Docs"))

	e.Update(edited, func(f *emulator.File) { f.Content = []byte("new and longer") })
	e.Update(renamed, func(f *emulator.File) { f.Name = "new-name.txt" })
	e.Update(trashed, func(f *emulator.File) { f.Trashed = true })
	e.Update(restored, func(f *emulator.File) { f.Trashed = false })
	e.WriteFile(docs, "new.txt", []byte("n"))

	want := []string{"edited.txt", "new-name.txt", "new.txt", "restored.txt"}
	waitFor(t, "the listing follows Drive", 20*time.Second, func() bool {
		return slices.Equal(dirNames(t, d.path("Docs")),
			want)
	})
	waitFor(t, "stat follows Drive", 20*time.Second, func() bool {
		fi, err := os.Stat(d.path("Docs/edited.txt"))
		return err == nil && fi.Size() == int64(len("new and longer"))
	})
	if got, err := os.ReadFile(d.path("Docs/edited.txt")); err != nil || string(got) != "new and longer" {
		t.Fatalf("edited.txt after: %q, %v", got, err)
	}
	if _, err := os.Stat(d.path("Docs/old-name.txt")); !os.IsNotExist(err) {
		t.Fatalf("old name after the rename: %v", err)
	}
}

func TestRestartCatchesUpAtOnce(t *testing.T) {
	e := startEmu(t)
	f := e.WriteFile(e.Root(), "before.txt", []byte("x"))
	d := newDaemon(t, e)
	d.start()
	d.stop()
	e.Update(f, func(f *emulator.File) { f.Name = "while-down.txt" })

	d.start()
	waitFor(t, "the change made while down", 5*time.Second, func() bool {
		return slices.Equal(dirNames(t, d.myDrive()), []string{"while-down.txt"})
	})
}

func TestChangedContentDropsCachedPages(t *testing.T) {
	e := startEmu(t)
	f := e.WriteFile(e.Root(), "f.txt", []byte("version one"))
	d := newDaemon(t, e)
	d.start()
	if got, err := os.ReadFile(d.path("f.txt")); err != nil || string(got) != "version one" {
		t.Fatalf("before: %q, %v", got, err)
	}
	old, _ := e.Get(f)
	e.Update(f, func(f *emulator.File) {
		f.Content = []byte("version two")
		f.Modified = old.Modified
	})
	waitFor(t, "the new bytes", 20*time.Second, func() bool {
		got, err := os.ReadFile(d.path("f.txt"))
		return err == nil && string(got) == "version two"
	})
}
