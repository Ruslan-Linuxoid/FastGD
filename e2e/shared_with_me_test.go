//go:build e2e

package e2e

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func TestSharedWithMeReadsWithoutTheNetwork(t *testing.T) {
	e := startEmu(t)
	shared := e.Put(emulator.File{Name: "s.txt", Content: []byte("shared"), SharedWithMe: true})
	e.Put(emulator.File{Name: "Plans", MimeType: emulator.MimeFolder, SharedWithMe: true})
	e.Put(emulator.File{Name: "link", MimeType: emulator.MimeShortcut, Parents: []string{e.Root()},
		ShortcutTarget: shared})
	d := newDaemon(t, e)
	d.start()
	if got, err := os.Readlink(d.path("link")); err != nil || got != "../Shared with me/s.txt" {
		t.Fatalf("the shortcut leads to %q, %v", got, err)
	}
	if err := os.WriteFile(d.path("link"), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the write through the shortcut to reach the shared file", 20*time.Second, func() bool {
		f, ok := e.Get(shared)
		return ok && string(f.Content) == "edited"
	})
	if got, err := os.ReadFile(d.path("/Shared with me/s.txt")); err != nil || string(got) != "edited" {
		t.Fatalf("the shared file reads %q, %v", got, err)
	}
	e.SetOffline(true)
	if got := dirNames(t, d.path("/Shared with me")); !slices.Equal(got, []string{"Plans", "s.txt"}) {
		t.Fatalf("without the network Shared with me lists %q", got)
	}
	if got, err := os.ReadFile(d.path("/Shared with me/s.txt")); err != nil || string(got) != "edited" {
		t.Fatalf("without the network the shared file reads %q, %v", got, err)
	}
}

func TestAnOlderTreeMovesUnderMyDrive(t *testing.T) {
	e := startEmu(t)
	workDocs(e)
	d := newDaemon(t, e)
	d.start()
	at := places(t, d)
	d.mustMount("add", "My Drive/Work/Docs", at+"/docs")
	d.stop()
	d.start()
	requireA(t, at+"/docs/a.txt")
	d.stop()
	root := e.Root()
	d.inDB(
		`DELETE FROM items WHERE ino = 1`,
		`DELETE FROM items WHERE id LIKE ':%'`,
		fmt.Sprintf(`UPDATE items SET ino = 1, name = '', dname = '', shown_as = '', parent = '', r_name = '',
			r_parent = '' WHERE id = '%s'`, root),
		fmt.Sprintf(`UPDATE items SET parent = '%s' WHERE parent = ':top'`, root),
		fmt.Sprintf(`UPDATE state SET value = 'older' WHERE key = '%s'`, meta.StateFields),
		`PRAGMA user_version = 25`,
	)
	d.start()
	requireA(t, d.path("Work/Docs/a.txt"))
	requireA(t, at+"/docs/a.txt")
	if out := d.mustMount("list"); !strings.Contains(out, "My Drive/Work/Docs") {
		t.Fatalf("mount list shows %q, want the binding under My Drive", out)
	}
}

func TestSharedWithMeIsTheCollection(t *testing.T) {
	e := startEmu(t)
	project := e.Put(emulator.File{Name: "Project", MimeType: emulator.MimeFolder, SharedWithMe: true})
	e.Put(emulator.File{Name: "plan.txt", Parents: []string{project}, Content: []byte("plan"), SharedWithMe: true})
	e.Put(emulator.File{Name: "a.txt", Content: []byte("right in"), SharedWithMe: true})
	e.Put(emulator.File{Name: "a.txt", Parents: []string{project}, Content: []byte("in the folder"),
		SharedWithMe: true})
	e.Put(emulator.File{Name: "opened.txt", Content: []byte("o"), OpenedByLink: true})
	e.Put(emulator.File{Name: "far.txt", Parents: []string{"folderOfAnotherOne"}, Content: []byte("far"),
		SharedWithMe: true})
	d := newDaemon(t, e)
	d.start()
	want := []string{"Project", "a (1).txt", "a.txt", "far.txt", "plan.txt"}
	if got := dirNames(t, d.path("/Shared with me")); !slices.Equal(got, want) {
		t.Fatalf("Shared with me lists %q, want %q", got, want)
	}
	for path, content := range map[string]string{"/Shared with me/plan.txt": "plan",
		"/Shared with me/Project/plan.txt": "plan", "/Shared with me/a.txt": "right in",
		"/Shared with me/a (1).txt": "in the folder", "/Shared with me/far.txt": "far"} {
		if got, err := os.ReadFile(d.path(path)); err != nil || string(got) != content {
			t.Errorf("%s reads %q, %v; want %q", path, got, err, content)
		}
	}
	if err := os.Remove(d.path("/Shared with me/plan.txt")); !errors.Is(err, syscall.EPERM) {
		t.Errorf("a removal by the second name: %v, want EPERM", err)
	}
	if err := os.Rename(d.path("/Shared with me/plan.txt"), d.path("/Shared with me/Project/p.txt")); !errors.Is(err,
		syscall.EPERM) {
		t.Errorf("a move by the second name: %v, want EPERM", err)
	}
	if _, err := os.Stat(d.path("/Shared with me/Project/plan.txt")); err != nil {
		t.Errorf("the file left its folder: %v", err)
	}
}
