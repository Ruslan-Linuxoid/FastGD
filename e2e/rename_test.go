//go:build e2e

package e2e

import (
	"errors"
	"os"
	"slices"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestRenameRemoveAndAttributes(t *testing.T) {
	e := startEmu(t)
	docs := e.Mkdir(e.Root(), "Docs")
	e.Mkdir(e.Root(), "Archive")
	report := e.WriteFile(docs, "report.txt", []byte("report"))
	old := e.WriteFile(docs, "old.txt", []byte("to be replaced"))
	draft := e.WriteFile(docs, "draft.txt", []byte("draft"))
	full := e.Mkdir(e.Root(), "Full")
	e.WriteFile(full, "uncached.bin", []byte("x"))
	empty := e.Mkdir(e.Root(), "Empty")
	d := newDaemon(t, e)
	d.start()

	requireRenamesAndRemovals(t, d)
	when := time.Date(2021, 4, 5, 6, 7, 8, 0, time.UTC)
	requireChangesOfAttributesAreNotKept(t, d, when)
	requireLinksAndExtendedAttributesRefused(t, d)

	waitFor(t, "Drive to follow", 20*time.Second, func() bool {
		r, _ := e.Get(report)
		o, _ := e.Get(old)
		dr, _ := e.Get(draft)
		em, _ := e.Get(empty)
		return r.Name == "report-2025.txt" && r.Modified.Equal(when) && o.Trashed && dr.Name == "old.txt" && em.Trashed
	})
	if f, _ := e.Get(full); f.Trashed {
		t.Fatal("the folder with an uncached file went to the trash")
	}
}

func requireRenamesAndRemovals(t *testing.T, d *daemon) {
	t.Helper()
	if err := os.Rename(d.path("Docs/report.txt"), d.path("Archive/report-2025.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(d.path("Docs/draft.txt"), d.path("Docs/old.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(d.path("Empty")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(d.path("Full")); !errors.Is(err, syscall.ENOTEMPTY) {
		t.Fatalf("rmdir of a folder with an uncached file: %v, want ENOTEMPTY", err)
	}
	if got := dirNames(t, d.path("Docs")); !slices.Equal(got, []string{"old.txt"}) {
		t.Fatalf("Docs lists %q", got)
	}
	if got, err := os.ReadFile(d.path("Docs/old.txt")); err != nil || string(got) != "draft" {
		t.Fatalf("old.txt after mv over it: %q, %v", got, err)
	}
}

func requireChangesOfAttributesAreNotKept(t *testing.T, d *daemon, when time.Time) {
	t.Helper()
	if err := os.Chtimes(d.path("Archive/report-2025.txt"), when, when); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(d.path("Archive/report-2025.txt"), 0o600); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if err := os.Chown(d.path("Archive/report-2025.txt"), 1, 1); err != nil {
		t.Fatalf("chown: %v", err)
	}
	fi, err := os.Stat(d.path("Archive/report-2025.txt"))
	if err != nil {
		t.Fatal(err)
	}
	st := fi.Sys().(*syscall.Stat_t)
	if fi.Mode() != 0o644 || st.Uid != testUID || st.Gid != testGID || !fi.ModTime().Equal(when) {
		t.Fatalf("after chmod, chown and utimes: mode %v, %d:%d, mtime %v", fi.Mode(), st.Uid, st.Gid, fi.ModTime())
	}
}

func requireLinksAndExtendedAttributesRefused(t *testing.T, d *daemon) {
	t.Helper()
	if err := os.Symlink("x", d.path("link")); !errors.Is(err, syscall.EPERM) {
		t.Fatalf("symlink: %v, want EPERM", err)
	}
	if err := os.Link(d.path("Docs/old.txt"), d.path("hard")); !errors.Is(err, syscall.EPERM) {
		t.Fatalf("link: %v, want EPERM", err)
	}
	if err := unix.Setxattr(d.path("Docs/old.txt"), "user.x", []byte("y"), 0); !errors.Is(err, unix.EOPNOTSUPP) {
		t.Fatalf("setxattr: %v, want EOPNOTSUPP", err)
	}
	if _, err := unix.Getxattr(d.path("Docs/old.txt"), "user.x", make([]byte, 16)); !errors.Is(err, unix.EOPNOTSUPP) {
		t.Fatalf("getxattr: %v, want EOPNOTSUPP", err)
	}
}

func TestRemovedOpenFileStillReads(t *testing.T) {
	e := startEmu(t)
	e.WriteFile(e.Root(), "f.txt", []byte("still here"))
	d := newDaemon(t, e)
	d.start()
	f, err := os.Open(d.path("f.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := os.Remove(d.path("f.txt")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 10)
	if _, err := f.ReadAt(buf, 0); err != nil || string(buf) != "still here" {
		t.Fatalf("read of a removed open file: %q, %v", buf, err)
	}
}
