//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func syncNow(t *testing.T, d *daemon) (string, bool) {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), daemonBin(t), "sync", "--config", filepath.Join(d.dir,
		"config.toml")).CombinedOutput()
	return string(out), err == nil
}

func TestSyncSendsWhatWaitsBeforeItReturns(t *testing.T) {
	e := startEmu(t)
	e.SetUploadFault(func(name string) int {
		if name == "stuck.txt" {
			return 400
		}
		return 0
	})
	d := newDaemon(t, e)
	d.start()
	f := syncTheOpenFile(t, e, d)

	if err := os.WriteFile(d.path("stuck.txt"), []byte("stuck"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, ok := syncNow(t, d)
	if ok || !strings.Contains(out, "stuck.txt: ") || !strings.Contains(out,
		"operations left that Drive did not take: 1") {
		t.Fatalf("sync with a file Drive refuses: ok %v\n%s", ok, out)
	}

	f.Close()
	d.stop()
	out, ok = syncNow(t, d)
	if ok || !strings.Contains(out, "the daemon is not running") {
		t.Fatalf("sync with no daemon: ok %v\n%s", ok, out)
	}
}

func syncTheOpenFile(t *testing.T, e *emu, d *daemon) *os.File {
	t.Helper()
	f, err := os.Create(d.path("open.txt"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { f.Close() })
	if _, err := f.WriteString("written so far"); err != nil {
		t.Fatal(err)
	}
	out, ok := syncNow(t, d)
	if !ok || !strings.Contains(out, "everything is in Drive") {
		t.Fatalf("sync with one open file: ok %v\n%s", ok, out)
	}
	if got, found := e.ByPath("open.txt"); !found || string(got.Content) != "written so far" {
		t.Fatalf("when sync returned, Drive held the open file as %q, found %v", got.Content, found)
	}
	return f
}
