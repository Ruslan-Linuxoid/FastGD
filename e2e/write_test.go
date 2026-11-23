//go:build e2e

package e2e

import (
	"bytes"
	"crypto/md5"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func waitInDrive(t *testing.T, e *emu, content []byte, path ...string) {
	t.Helper()
	waitFor(t, "the upload of "+filepath.Join(path...), 20*time.Second, func() bool {
		f, ok := e.ByPath(path...)
		return ok && md5.Sum(f.Content) == md5.Sum(content)
	})
}

func TestWrittenFilesReachDrive(t *testing.T) {
	e := startEmu(t)
	d := newDaemon(t, e)
	d.start()

	if err := os.MkdirAll(d.path("a/b/c"), 0o755); err != nil {
		t.Fatal(err)
	}
	big := testenv.RandomBytes(3<<20+5, 40)
	if err := os.WriteFile(d.path("a/b/c/big.bin"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(d.path("a/b/c/big.bin")); err != nil || !bytes.Equal(got, big) {
		t.Fatalf("read back right after writing: %v", err)
	}
	if err := os.WriteFile(d.path("empty.txt"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	waitInDrive(t, e, big, "a", "b", "c", "big.bin")
	waitInDrive(t, e, nil, "empty.txt")
}

func TestWritingWithDriveDown(t *testing.T) {
	e := startEmu(t)
	d := newDaemon(t, e)
	d.start()
	e.SetOffline(true)
	if err := os.Mkdir(d.path("offline"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(d.path("offline/note.txt"), []byte("written offline"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(d.path("offline/note.txt")); err != nil || string(got) != "written offline" {
		t.Fatalf("read back with Drive down: %q, %v", got, err)
	}
	e.SetOffline(false)
	waitInDrive(t, e, []byte("written offline"), "offline", "note.txt")
}

func TestSpeedCapsOfTheConfigHold(t *testing.T) {
	e := startEmu(t)
	down := testenv.RandomBytes(1<<20, 86)
	e.WriteFile(e.Root(), "down.bin", down)
	d := newDaemon(t, e)
	cfg, err := os.OpenFile(filepath.Join(d.dir, "config.toml"), os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cfg.WriteString("max_download_speed = \"512K\"\nmax_upload_speed = \"512K\"\n"); err != nil {
		t.Fatal(err)
	}
	cfg.Close()
	d.start()

	start := time.Now()
	if got, err := os.ReadFile(d.path("down.bin")); err != nil || !bytes.Equal(got, down) {
		t.Fatalf("down.bin reads wrong: %v", err)
	}
	if took := time.Since(start); took < 1500*time.Millisecond {
		t.Fatalf("1 MiB came down in %v with downloads held to 512 KiB/s", took)
	}

	up := testenv.RandomBytes(2<<20, 87)
	if err := os.WriteFile(d.path("up.bin"), up, 0o644); err != nil {
		t.Fatal(err)
	}
	start = time.Now()
	waitInDrive(t, e, up, "up.bin")
	if took := time.Since(start); took < 3500*time.Millisecond {
		t.Fatalf("2 MiB went up %v after the write with uploads held to 512 KiB/s", took)
	}
}
