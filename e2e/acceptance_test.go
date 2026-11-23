//go:build e2e

package e2e

import (
	"bytes"
	"crypto/md5"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func readPast(t *testing.T, path string) []byte {
	t.Helper()
	fd, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer fd.Close()
	if err := unix.Fadvise(int(fd.Fd()), 0, 0, unix.FADV_DONTNEED); err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(fd)
	if err != nil {
		t.Fatalf("%s: %v", path, err)
	}
	return got
}

func mediaRequests(e *emu, id string) int {
	n := 0
	for _, r := range e.Requests() {
		if r.Query.Get("alt") == "media" && strings.Contains(r.Path, id) {
			n++
		}
	}
	return n
}

func TestAcceptanceWrittenFileReadsBackAndReachesDrive(t *testing.T) {
	e := startEmu(t)
	d := newDaemon(t, e)
	d.start()
	content := testenv.RandomBytes(5<<20+123, 900)
	if err := os.WriteFile(d.path("report.bin"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(d.path("report.bin")); err != nil || !bytes.Equal(got, content) {
		t.Fatalf("read back right after the write: %v", err)
	}
	waitInDrive(t, e, content, "report.bin")
}

func TestAcceptanceFilesOutliveARestart(t *testing.T) {
	e := startEmu(t)
	d := newDaemon(t, e)
	d.start()
	sent := testenv.RandomBytes(1<<20, 901)
	if err := os.WriteFile(d.path("sent.bin"), sent, 0o644); err != nil {
		t.Fatal(err)
	}
	waitInDrive(t, e, sent, "sent.bin")

	e.SetOffline(true)
	unsent := testenv.RandomBytes(1<<20, 902)
	if err := os.WriteFile(d.path("unsent.bin"), unsent, 0o644); err != nil {
		t.Fatal(err)
	}
	d.stop()
	e.SetOffline(false)
	if _, ok := e.ByPath("unsent.bin"); ok {
		t.Fatal("scenario not built: the second file reached Drive before the stop")
	}
	d.start()
	for name, want := range map[string][]byte{"sent.bin": sent, "unsent.bin": unsent} {
		if got, err := os.ReadFile(d.path(name)); err != nil || !bytes.Equal(got, want) {
			t.Fatalf("%s after the restart: %v", name, err)
		}
	}
	waitInDrive(t, e, unsent, "unsent.bin")
}

func TestAcceptanceWorksWithDriveDownAndUploadsWhenItIsBack(t *testing.T) {
	e := startEmu(t)
	cached := testenv.RandomBytes(1<<20, 903)
	e.WriteFile(e.Root(), "cached.bin", cached)
	d := newDaemon(t, e)
	d.start()
	if got, err := os.ReadFile(d.path("cached.bin")); err != nil || !bytes.Equal(got, cached) {
		t.Fatalf("the first read of the file: %v", err)
	}
	e.SetOffline(true)
	if got := readPast(t, d.path("cached.bin")); !bytes.Equal(got, cached) {
		t.Fatal("the cached file reads different with Drive down")
	}
	written := writeFilesWithDriveDown(t, d)

	waitFor(t, "five dirty files with Drive down", 30*time.Second, func() bool {
		return strings.Contains(status(t, d), "dirty files:  5,")
	})
	requireDirtyFilesReadAndStayOutOfDrive(t, e, d, written)
	e.SetOffline(false)
	for name, want := range written {
		waitFor(t, "the upload of "+name+" after Drive came back", time.Minute, func() bool {
			f, ok := e.ByPath(name)
			return ok && md5.Sum(f.Content) == md5.Sum(want)
		})
	}
	waitFor(t, "no dirty file left", 30*time.Second, func() bool {
		return strings.Contains(status(t, d), "dirty files:  0,")
	})
}

func writeFilesWithDriveDown(t *testing.T, d *daemon) map[string][]byte {
	t.Helper()
	written := map[string][]byte{}
	for i := range 5 {
		name := fmt.Sprintf("offline%d.bin", i)
		written[name] = testenv.RandomBytes(200<<10, uint64(910+i))
		if err := os.WriteFile(d.path(name), written[name], 0o644); err != nil {
			t.Fatalf("a write with Drive down: %v", err)
		}
	}
	return written
}

func requireDirtyFilesReadAndStayOutOfDrive(t *testing.T, e *emu, d *daemon, written map[string][]byte) {
	t.Helper()
	for name, want := range written {
		if got := readPast(t, d.path(name)); !bytes.Equal(got, want) {
			t.Fatalf("%s reads different with Drive down", name)
		}
		if _, ok := e.ByPath(name); ok {
			t.Fatalf("%s is in Drive with Drive down", name)
		}
	}
}

func TestAcceptanceFullCacheEvictsAndFetchesAgain(t *testing.T) {
	const limit = 8 << 20
	e := startEmu(t)
	contents := map[string][]byte{}
	ids := map[string]string{}
	for i := range 10 {
		name := fmt.Sprintf("f%d.bin", i)
		contents[name] = testenv.RandomBytes(2<<20, uint64(920+i))
		ids[name] = e.WriteFile(e.Root(), name, contents[name])
	}
	d := newDaemon(t, e)
	d.cacheLimit = limit
	d.start()
	dirty, open := holdADirtyAndAnOpenFile(t, e, d)
	defer open.Close()

	for i := 1; i < 10; i++ {
		name := fmt.Sprintf("f%d.bin", i)
		if got, err := os.ReadFile(d.path(name)); err != nil || !bytes.Equal(got, contents[name]) {
			t.Fatalf("%s: %v", name, err)
		}
	}
	requireCacheBackWithinLimit(t, d, limit, time.Now())
	if got := readPast(t, d.path("dirty.bin")); !bytes.Equal(got, dirty) {
		t.Fatal("the dirty file reads different after the eviction")
	}
	requireOpenFileStaysInTheCache(t, e, d, contents["f0.bin"], ids["f0.bin"])

	for name, want := range contents {
		if got := readPast(t, d.path(name)); !bytes.Equal(got, want) {
			t.Fatalf("%s reads different after the eviction", name)
		}
	}
	if mediaRequests(e, "") == 0 {
		t.Fatal("no file was fetched again: nothing was evicted")
	}
}

func holdADirtyAndAnOpenFile(t *testing.T, e *emu, d *daemon) ([]byte, *os.File) {
	t.Helper()

	dirty := testenv.RandomBytes(3<<20, 940)
	e.SetUploadFault(func(name string) int {
		if name == "dirty.bin" {
			return 503
		}
		return 0
	})
	if err := os.WriteFile(d.path("dirty.bin"), dirty, 0o644); err != nil {
		t.Fatal(err)
	}
	open, err := os.Open(d.path("f0.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := open.ReadAt(make([]byte, 2<<20), 0); err != nil {
		open.Close()
		t.Fatal(err)
	}
	return dirty, open
}

func requireCacheBackWithinLimit(t *testing.T, d *daemon, limit int64, passed time.Time) {
	t.Helper()

	const kept = 3<<20 + 2<<20 + 12*4096
	waitFor(t, "the cache within its limit and what is not evicted", 12*time.Second, func() bool {
		return d.contentDU() <= limit+kept
	})
	t.Logf("the cache was back within its limit %v after the last read", time.Since(passed).Round(time.Millisecond))
}

func requireOpenFileStaysInTheCache(t *testing.T, e *emu, d *daemon, want []byte, id string) {
	t.Helper()

	e.ResetRequests()
	if got := readPast(t, d.path("f0.bin")); !bytes.Equal(got, want) || mediaRequests(e, id) != 0 {
		t.Fatalf("the open file was evicted: reading it again took %d downloads", mediaRequests(e, id))
	}
}
