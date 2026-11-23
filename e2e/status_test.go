//go:build e2e

package e2e

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDfShowsTheDriveQuota(t *testing.T) {
	e := startEmu(t)
	e.SetQuota(15<<30, 4<<30)
	d := newDaemon(t, e)
	d.start()
	waitFor(t, "df to show the quota", 10*time.Second, func() bool {
		out, err := exec.CommandContext(t.Context(), "df", "-B1", "--output=size,avail", d.mnt).Output()
		if err != nil {
			return false
		}
		fields := strings.Fields(string(out))
		if len(fields) != 4 {
			return false
		}
		size, _ := strconv.ParseInt(fields[2], 10, 64)
		avail, _ := strconv.ParseInt(fields[3], 10, 64)
		return size == 15<<30 && avail == 11<<30
	})
	if err := os.WriteFile(d.path("f.bin"), make([]byte, 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "df to count the upload", 20*time.Second, func() bool {
		out, err := exec.CommandContext(t.Context(), "df", "-B1", "--output=avail", d.mnt).Output()
		if err != nil {
			return false
		}
		fields := strings.Fields(string(out))
		avail, _ := strconv.ParseInt(fields[len(fields)-1], 10, 64)
		return avail == 11<<30-1<<20
	})
}

func status(t *testing.T, d *daemon) string {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), daemonBin(t), "status", "--config", filepath.Join(d.dir,
		"config.toml")).CombinedOutput()
	if err != nil {
		t.Fatalf("status: %v\n%s", err, out)
	}
	return string(out)
}

func TestStatusWithTheDaemonUpAndDown(t *testing.T) {
	e := startEmu(t)
	e.SetUploadFault(func(name string) int {
		if name == "stuck.txt" {
			return 503
		}
		return 0
	})
	d := newDaemon(t, e)
	d.start()
	if err := os.WriteFile(d.path("stuck.txt"), []byte("never goes up"), 0o644); err != nil {
		t.Fatal(err)
	}

	waitFor(t, "status to show the failing upload", 45*time.Second, func() bool {
		out := status(t, d)
		return strings.Contains(out, "dirty files:  1,") && strings.Contains(out, "stuck.txt") &&
			strings.Contains(out, "503") && !strings.Contains(out, "last poll:    never")
	})
	d.stop()
	requireStatusOfAStoppedDaemonLeavesTheDatabaseAlone(t, d)
	requireStatusAfterKillNine(t, d)
}

func requireStatusOfAStoppedDaemonLeavesTheDatabaseAlone(t *testing.T, d *daemon) {
	t.Helper()
	db := filepath.Join(d.cache, "meta.db")
	before, err := os.Stat(db)
	if err != nil {
		t.Fatal(err)
	}
	if out := status(t, d); !strings.Contains(out, "stuck.txt") {
		t.Fatalf("status with the daemon stopped:\n%s", out)
	}
	if after, err := os.Stat(db); err != nil || after.Size() != before.Size() || !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("status changed the database: %v", err)
	}
}

func requireStatusAfterKillNine(t *testing.T, d *daemon) {
	t.Helper()

	d.start()
	if err := d.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-d.done
	if out := status(t, d); !strings.Contains(out, "stuck.txt") {
		t.Fatalf("status after kill -9:\n%s", out)
	}
}

func TestStatusShowsAFileGrowingWhileWritten(t *testing.T) {
	e := startEmu(t)
	d := newDaemon(t, e)
	d.start()
	f, err := os.Create(d.path("growing.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(make([]byte, 3<<20)); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "status to show the size while the file is open", 10*time.Second, func() bool {
		return strings.Contains(status(t, d), "dirty files:  1, 3.0 MiB")
	})
}

func TestDownloadQuotaShowsInStatus(t *testing.T) {
	e := startEmu(t)
	id := e.WriteFile(e.Root(), "refused.bin", make([]byte, 64<<10))
	e.WriteFile(e.Root(), "free.bin", []byte("free"))
	d := newDaemon(t, e)
	d.start()
	e.SetFileDownloadQuotaExceeded(id, true)
	if _, err := os.ReadFile(d.path("refused.bin")); !errors.Is(err, syscall.EIO) {
		t.Fatalf("reading a file over the download quota: %v, want EIO", err)
	}
	if got, err := os.ReadFile(d.path("free.bin")); err != nil || string(got) != "free" {
		t.Fatalf("another file reads %q, %v", got, err)
	}

	waitFor(t, "status to show the refusal and the download", 30*time.Second, func() bool {
		out := status(t, d)
		return strings.Contains(out, "refused by the download quota at ") && strings.Contains(out, "refused.bin") &&
			strings.Contains(out, "downloaded:   4 B in 24 h") && !strings.Contains(out, "quota units:  0 ")
	})
}
