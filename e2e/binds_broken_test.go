//go:build e2e

package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestABrokenListOfBindingsLeavesTheMountUp(t *testing.T) {
	e := startEmu(t)
	workDocs(e)
	d := newDaemon(t, e)
	if err := os.MkdirAll(d.cache, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d.cache, "mounts.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	d.start()
	requireA(t, d.path("Work/Docs/a.txt"))
	want := filepath.Join(d.cache, "mounts.json") + " cannot be read"
	if out, err := d.mountCmd("list"); err == nil || !strings.Contains(out, want) {
		t.Fatalf("mount list: %v\n%s", err, out)
	}
	out, err := exec.CommandContext(t.Context(), daemonBin(t), "status", "--config",
		filepath.Join(d.dir, "config.toml")).CombinedOutput()
	if err != nil || !strings.Contains(string(out), want) {
		t.Fatalf("status: %v\n%s", err, out)
	}
}
