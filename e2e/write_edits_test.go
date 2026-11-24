//go:build e2e

package e2e

import (
	"bytes"
	"crypto/md5"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestEditOfAFileFromDriveReachesDrive(t *testing.T) {
	e := startEmu(t)
	content := testenv.RandomBytes(3<<20, 83)
	id := e.WriteFile(e.Root(), "edit.bin", content)
	d := newDaemon(t, e)
	d.start()
	want := append([]byte(nil), content...)
	f, err := os.OpenFile(d.path("edit.bin"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	patch := []byte("patched in the middle of a page")
	if _, err := f.WriteAt(patch, 1<<20+100); err != nil {
		t.Fatal(err)
	}
	copy(want[1<<20+100:], patch)
	tail := testenv.RandomBytes(5000, 84)
	if _, err := f.WriteAt(tail, int64(len(content))); err != nil {
		t.Fatal(err)
	}
	want = append(want, tail...)
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	waitInDrive(t, e, want, "edit.bin")
	if got, err := os.ReadFile(d.path("edit.bin")); err != nil || md5.Sum(got) != md5.Sum(want) {
		t.Fatalf("the edited file reads back different: %v", err)
	}
	if err := os.Truncate(d.path("edit.bin"), 1000); err != nil {
		t.Fatal(err)
	}
	waitInDrive(t, e, want[:1000], "edit.bin")
	if f, _ := e.Get(id); len(f.Content) != 1000 {
		t.Fatalf("Drive holds %d bytes after the truncation", len(f.Content))
	}
	if err := os.Truncate(d.path("edit.bin"), 6000); err != nil {
		t.Fatal(err)
	}
	waitInDrive(t, e, append(want[:1000:1000], make([]byte, 5000)...), "edit.bin")
}

func TestEditOfABarelyCachedFileSurvivesAChangeInDrive(t *testing.T) {
	e := startEmu(t)
	const size = 100 << 20
	content := testenv.RandomBytes(size, 85)
	id := e.WriteFile(e.Mkdir(e.Root(), "docs"), "big.bin", content)
	d := newDaemon(t, e)
	d.start()
	base := e.Revisions(id)[0].ID
	changed, want := editWhileDriveChangesBig(t, e, d, id, content)
	lost := d.path("/Lost and Found (FastGD)/My Drive/docs")
	requireWholeLocalVersionInLostAndFound(t, lost, want)
	if got, err := os.ReadFile(d.path("docs/big.bin")); err != nil || !bytes.Equal(got, changed) {
		t.Fatalf("the file does not read as the version put in Drive: %v", err)
	}
	requireNothingCameFromARevision(t, e, base)
	if got, _ := e.Get(id); !bytes.Equal(got.Content, changed) {
		t.Fatal("the edit went over the version put in Drive after it")
	}
	requireLostAndFoundIsCountedAndStaysOutOfDrive(t, e, d)
	requireVersionTakenOutOfLostAndFoundGoesToDrive(t, e, d, lost, want)
}

func editWhileDriveChangesBig(t *testing.T, e *emu, d *daemon, id string, content []byte) (changed, want []byte) {
	t.Helper()
	f, err := os.OpenFile(d.path("docs/big.bin"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.ReadAt(make([]byte, 4<<10), 0); err != nil {
		t.Fatal(err)
	}
	patch := testenv.RandomBytes(1<<10, 86)
	if _, err := f.WriteAt(patch, 50<<20+100); err != nil {
		t.Fatal(err)
	}
	changed = testenv.RandomBytes(1<<20, 87)

	e.ResetRequests()
	e.Update(id, func(f *emulator.File) { f.Content = changed })
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	want = content
	copy(want[50<<20+100:], patch)
	return changed, want
}

func requireWholeLocalVersionInLostAndFound(t *testing.T, lost string, want []byte) {
	t.Helper()
	waitFor(t, "the whole local version in Lost and Found", 2*time.Minute, func() bool {
		names, _ := filepath.Glob(lost + "/big (конфликт *).bin")
		if len(names) != 1 {
			return false
		}
		got, err := os.ReadFile(names[0])
		return err == nil && md5.Sum(got) == md5.Sum(want)
	})
}

func requireNothingCameFromARevision(t *testing.T, e *emu, base string) {
	t.Helper()

	for _, r := range e.Requests() {
		if strings.HasSuffix(r.Path, "/revisions/"+base) && r.Query.Get("alt") == "media" {
			t.Fatalf("a part of the version the edit was made on came from its revision: %s", r.Path)
		}
	}
}

func requireLostAndFoundIsCountedAndStaysOutOfDrive(t *testing.T, e *emu, d *daemon) {
	t.Helper()
	if out := status(t, d); !strings.Contains(out, `lost & found: 1 in "Lost and Found (FastGD)"`) {
		t.Fatalf("status does not count the file in Lost and Found:\n%s", out)
	}
	for _, f := range e.All() {
		if strings.Contains(f.Name, "конфликт") || strings.HasPrefix(f.Name, "Lost and Found") {
			t.Fatalf("%q went to Drive", f.Name)
		}
	}
}

func requireVersionTakenOutOfLostAndFoundGoesToDrive(t *testing.T, e *emu, d *daemon, lost string, want []byte) {
	t.Helper()

	names, _ := filepath.Glob(lost + "/big (конфликт *).bin")
	if err := os.Rename(names[0], d.path("docs/mine.bin")); err != nil {
		t.Fatal(err)
	}
	waitInDrive(t, e, want, "docs", "mine.bin")
	if _, err := os.Stat(d.path("/Lost and Found (FastGD)")); !os.IsNotExist(err) {
		t.Fatalf("Lost and Found is still there, emptied: %v", err)
	}
}

func TestSavesByRenameKeepTheFileID(t *testing.T) {
	e := startEmu(t)
	id := e.WriteFile(e.Root(), "doc.txt", []byte("version 1"))
	d := newDaemon(t, e)
	d.start()
	for n := 2; n <= 3; n++ {
		requireSaveByRename(t, e, d, id, n)
	}
	requireSaveByMovingAsideMakesANewFile(t, e, d, id)
}

func requireSaveByRename(t *testing.T, e *emu, d *daemon, id string, n int) {
	t.Helper()
	doc := d.path("doc.txt")
	want := []byte(fmt.Sprintf("version %d", n))
	tmp := d.path(".goutputstream-A1B2C3")
	if err := os.WriteFile(tmp, want, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, doc); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(doc); err != nil || string(got) != string(want) {
		t.Fatalf("after save %d the document reads %q: %v", n-1, got, err)
	}
	waitInDrive(t, e, want, "doc.txt")
	if f, _ := e.Get(id); string(f.Content) != string(want) || f.Trashed {
		t.Fatalf("after save %d the file of Drive holds %q, trashed %v", n-1, f.Content, f.Trashed)
	}
	if got := len(e.Revisions(id)); got != n {
		t.Fatalf("after save %d the file has %d revisions, want %d", n-1, got, n)
	}
	if all := e.All(); len(all) != 1 {
		t.Fatalf("after save %d Drive holds %d items, want the document alone", n-1, len(all))
	}
}

func requireSaveByMovingAsideMakesANewFile(t *testing.T, e *emu, d *daemon, id string) {
	t.Helper()
	doc := d.path("doc.txt")
	want := []byte("version 4")
	tmp, old := doc+"___jb_tmp___", doc+"___jb_old___"
	if err := os.WriteFile(tmp, want, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, step := range []func() error{
		func() error { return os.Rename(doc, old) },
		func() error { return os.Rename(tmp, doc) },
		func() error { return os.Remove(old) },
	} {
		if err := step(); err != nil {
			t.Fatal(err)
		}
	}
	waitInDrive(t, e, want, "doc.txt")
	waitFor(t, "the file moved aside to reach the trash", 20*time.Second, func() bool {
		f, _ := e.Get(id)
		return f.Trashed
	})
	if f, ok := e.ByPath("doc.txt"); !ok || f.ID == id {
		t.Fatalf("after the save by moving aside doc.txt in Drive is the file %s, the one it was", f.ID)
	}
}

func TestTruncateOfAFileFromDriveNotFetchedReachesDrive(t *testing.T) {
	e := startEmu(t)
	content := testenv.RandomBytes(300<<10, 88)
	e.WriteFile(e.Root(), "cut.bin", content)
	d := newDaemon(t, e)
	d.start()
	if err := os.Truncate(d.path("cut.bin"), 100000); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(d.path("cut.bin"), 150000); err != nil {
		t.Fatal(err)
	}
	want := append(append([]byte(nil), content[:100000]...), make([]byte, 50000)...)

	waitInDrive(t, e, want, "cut.bin")
	if got, err := os.ReadFile(d.path("cut.bin")); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("the file cut and grown reads %d bytes, not the start of its Drive version with zeros after it: %v",
			len(got), err)
	}
}
