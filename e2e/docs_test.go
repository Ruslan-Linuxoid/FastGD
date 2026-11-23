//go:build e2e

package e2e

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
)

func xattrs(t *testing.T, path string) map[string]string {
	t.Helper()

	again := func(call func() (int, error)) (n int, err error) {
		n, err = call()
		for errors.Is(err, unix.EINTR) {
			n, err = call()
		}
		return n, err
	}
	buf := make([]byte, 4096)
	n, err := again(func() (int, error) { return unix.Listxattr(path, buf) })
	if err != nil {
		t.Fatalf("listxattr %s: %v", path, err)
	}
	out := map[string]string{}
	for name := range strings.SplitSeq(strings.TrimSuffix(string(buf[:n]), "\x00"), "\x00") {
		if name == "" {
			continue
		}
		val := make([]byte, 4096)
		m, err := again(func() (int, error) { return unix.Getxattr(path, name, val) })
		if err != nil {
			t.Fatalf("getxattr %s of %s: %v", name, path, err)
		}
		out[name] = string(val[:m])
	}
	return out
}

func TestLinkFilesThroughTheKernel(t *testing.T) {
	e := startEmu(t)
	doc := e.WriteGoogleFile(e.Root(), "Report", "application/vnd.google-apps.document")
	form := e.WriteGoogleFile(e.Root(), "Poll", "application/vnd.google-apps.form")
	e.WriteFile(e.Root(), "plain.txt", []byte("x"))
	d := newDaemon(t, e)
	d.start()

	if got := dirNames(t, d.path("")); !slices.Equal(got, []string{"Poll.gform", "Report.gdoc", "plain.txt"}) {
		t.Fatalf("the root lists %q", got)
	}
	requireLinkFileReadsItsPage(t, d, form)
	requireLinkFilesCarryTheAddress(t, d, doc, form)

	if err := os.Rename(d.path("Report.gdoc"), d.path("Notes.gdoc")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "Drive to take the new name", 20*time.Second, func() bool {
		f, _ := e.Get(doc)
		return f.Name == "Notes"
	})
	if err := os.Remove(d.path("Notes.gdoc")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "Drive to trash the document", 20*time.Second, func() bool {
		f, _ := e.Get(doc)
		return f.Trashed
	})
}

func requireLinkFileReadsItsPage(t *testing.T, d *daemon, form string) {
	t.Helper()
	page, err := os.ReadFile(d.path("Report.gdoc"))
	if err != nil {
		t.Fatalf("a read of a link file: %v", err)
	}
	url := xattrs(t, d.path("Report.gdoc"))["user.drive.item_open_url"]
	if !strings.HasPrefix(string(page), "<!DOCTYPE html>") || url == "" ||
		!strings.Contains(string(page), "url="+url+`"`) {
		t.Fatalf("the link file of %s reads %q", url, page)
	}
	fi, err := os.Stat(d.path("Report.gdoc"))
	if err != nil || fi.Size() != int64(len(page)) || fi.Mode() != 0o444 {
		t.Fatalf("the link file: %v, %v; want a read-only file of %d bytes", fi, err, len(page))
	}
	if got, err := os.ReadFile(d.path("Poll.gform")); err != nil ||
		!strings.Contains(string(got), "url=https://docs.google.com/forms/d/"+form+`/edit"`) {
		t.Fatalf("the link file of the form reads %q, %v", got, err)
	}
	requireLinkFileTakesNoWrites(t, d, page)
}

func requireLinkFileTakesNoWrites(t *testing.T, d *daemon, page []byte) {
	t.Helper()
	if err := os.WriteFile(d.path("Report.gdoc"), []byte("x"), 0o644); !errors.Is(err, syscall.EACCES) {
		t.Fatalf("a write to a link file: %v, want EACCES", err)
	}
	if err := os.Truncate(d.path("Report.gdoc"), 0); !errors.Is(err, syscall.EACCES) {
		t.Fatalf("truncate of a link file: %v, want EACCES", err)
	}
	copied := filepath.Join(t.TempDir(), "copy.gdoc")
	if out, err := exec.CommandContext(t.Context(), "cp", d.path("Report.gdoc"), copied).CombinedOutput(); err != nil {
		t.Fatalf("cp of a link file: %v: %s", err, out)
	}
	if got, err := os.ReadFile(copied); err != nil || string(got) != string(page) {
		t.Fatalf("the copy reads %q, %v", got, err)
	}
	if _, err := os.Create(d.path("New.gdoc")); !errors.Is(err, syscall.EPERM) {
		t.Fatalf("creating a link file: %v, want EPERM", err)
	}
}

func requireLinkFilesCarryTheAddress(t *testing.T, d *daemon, doc, form string) {
	t.Helper()

	waitFor(t, "the account's attribute", 20*time.Second, func() bool {
		return xattrs(t, d.path("Report.gdoc"))["user.drive.email"] == emulator.Email
	})
	want := map[string]string{
		"user.drive.gdoc":          "1",
		"user.drive.item_open_url": "https://docs.google.com/document/d/" + doc,
		"user.drive.email":         emulator.Email,
	}
	if got := xattrs(t, d.path("Report.gdoc")); len(got) != len(want) || got["user.drive.gdoc"] != "1" ||
		got["user.drive.item_open_url"] != want["user.drive.item_open_url"] {
		t.Fatalf("the attributes of the Doc are %q, want %q", got, want)
	}
	if got := xattrs(t,
		d.path("Poll.gform"))["user.drive.item_open_url"]; got != emulator.ViewLink("application/vnd.google-apps.form",
		form) {
		t.Fatalf("the address of the form is %q", got)
	}
	if got := xattrs(t, d.path("plain.txt")); len(got) != 0 {
		t.Fatalf("a file has the attributes %q", got)
	}
	if err := unix.Setxattr(d.path("Report.gdoc"), "user.drive.gdoc", []byte("0"), 0); !errors.Is(err, unix.EOPNOTSUPP) &&
		!errors.Is(err, unix.EACCES) {
		t.Fatalf("setxattr of a link file: %v, want a refusal", err)
	}
}
