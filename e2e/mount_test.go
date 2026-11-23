//go:build e2e

package e2e

import (
	"errors"
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name()
	}
	return names
}

func TestReadOnlyMount(t *testing.T) {
	e := startEmu(t)
	mtime := time.Date(2025, 6, 7, 8, 9, 10, 0, time.UTC)
	photosMtime := time.Date(2024, 3, 4, 5, 6, 7, 0, time.UTC)
	photos := e.Put(emulator.File{Name: "Photos", MimeType: emulator.MimeFolder, Parents: []string{e.Root()},
		Modified: photosMtime})
	big := testenv.RandomBytes(48<<20+12345, 7)
	e.Put(emulator.File{Name: "video.bin", MimeType: "application/octet-stream", Parents: []string{photos}, Content: big,
		Modified: mtime})
	e.WriteFile(photos, "small.txt", []byte("hello"))
	e.Mkdir(photos, "2025")
	d := newDaemon(t, e)
	d.start()

	t.Run("ls", func(t *testing.T) { requireListing(t, d) })
	t.Run("find", func(t *testing.T) { requireFind(t, d) })
	t.Run("stat", func(t *testing.T) { requireStat(t, d, len(big), mtime, photosMtime) })
	t.Run("read from the middle", func(t *testing.T) { requireReadFromMiddle(t, e, d, big) })
	t.Run("read anywhere", func(t *testing.T) { requireReadAnywhere(t, d, big) })
	t.Run("cat", func(t *testing.T) { requireCat(t, d, big) })
}

func TestMetadataWithDriveDown(t *testing.T) {
	e := startEmu(t)
	dir := e.Mkdir(e.Root(), "dir")
	e.WriteFile(dir, "f.txt", []byte("12345"))
	d := newDaemon(t, e)
	d.start()
	d.stop()

	e.SetOffline(true)
	d.start()
	if got := dirNames(t, d.path("dir")); !slices.Equal(got, []string{"f.txt"}) {
		t.Fatalf("dir lists %q", got)
	}
	fi, err := os.Stat(d.path("dir/f.txt"))
	if err != nil || fi.Size() != 5 {
		t.Fatalf("stat: %v, %v", fi, err)
	}
	out, err := exec.CommandContext(t.Context(), "find", d.mnt, "-mindepth", "1").Output()
	if lines := strings.Split(strings.TrimSpace(string(out)), "\n"); err != nil || len(lines) < 2 ||
		lines[1] != d.path("dir/f.txt") {
		t.Fatalf("find: %v\n%s", err, out)
	}

	start := time.Now()
	if _, err := os.ReadFile(d.path("dir/f.txt")); !errors.Is(err, syscall.EIO) {
		t.Fatalf("reading content with Drive down: %v, want EIO", err)
	}
	if took := time.Since(start); took > 30*time.Second {
		t.Fatalf("reading content with Drive down failed after %v", took)
	}
}

func TestSigtermUnmountsCleanly(t *testing.T) {
	e := startEmu(t)
	e.WriteFile(e.Root(), "f.txt", []byte("x"))
	d := newDaemon(t, e)

	d.start()
	d.stop()
	requireCleanMountPoint(t, d)

	d.start()
	f, err := os.Open(d.path("f.txt"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	d.stop()
	requireCleanMountPoint(t, d)
}

func requireCleanMountPoint(t *testing.T, d *daemon) {
	t.Helper()
	if mounted(d.mnt) {
		t.Fatal("still mounted after SIGTERM")
	}
	if _, err := os.ReadDir(d.mnt); err != nil {
		t.Fatalf("mount point after SIGTERM: %v", err)
	}
}

func TestRestartMountsFromDatabase(t *testing.T) {
	e := startEmu(t)
	e.WriteFile(e.Root(), "f.txt", []byte("x"))
	d := newDaemon(t, e)
	d.start()
	d.stop()

	e.ResetRequests()
	d.start()
	if got := dirNames(t, d.mnt); !slices.Equal(got, []string{"f.txt"}) {
		t.Fatalf("root lists %q", got)
	}
	for _, r := range e.Requests() {
		if r.Path == "/drive/v3/files" {
			t.Fatalf("restart listed Drive again: %+v", r)
		}
	}
}

func TestRestartAfterKillClearsDeadMount(t *testing.T) {
	e := startEmu(t)
	e.WriteFile(e.Root(), "f.txt", []byte("x"))
	d := newDaemon(t, e)
	d.start()
	if err := d.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-d.done
	if _, err := os.ReadDir(d.mnt); !errors.Is(err, syscall.ENOTCONN) {
		t.Fatalf("after kill -9 the mount point gives %v, want ENOTCONN", err)
	}
	d.start()
	if got := dirNames(t, d.mnt); !slices.Equal(got, []string{"f.txt"}) {
		t.Fatalf("root lists %q", got)
	}
	d.stop()
	requireCleanMountPoint(t, d)
}

func TestSecondDaemonOnLiveMountRefuses(t *testing.T) {
	e := startEmu(t)
	d := newDaemon(t, e)
	d.start()
	out, err := d.command().CombinedOutput()
	if err == nil || !strings.Contains(string(out), "already mounted") {
		t.Fatalf("second daemon: %v\n%s", err, out)
	}
	if got := dirNames(t, d.mnt); len(got) != 0 {
		t.Fatalf("first mount lists %q", got)
	}
}

func TestReadOfASilentDownloadFailsInTime(t *testing.T) {
	e := startEmu(t)
	e.WriteFile(e.Root(), "f.bin", testenv.RandomBytes(1<<20, 9))
	d := newDaemon(t, e)
	d.start()

	e.StallMedia(0)
	start := time.Now()
	if _, err := os.ReadFile(d.path("f.bin")); !errors.Is(err, syscall.EIO) {
		t.Fatalf("reading a download gone silent: %v, want EIO", err)
	}
	if took := time.Since(start); took > 30*time.Second {
		t.Fatalf("reading a download gone silent failed after %v", took)
	}
	e.StallMedia(-1)
}
