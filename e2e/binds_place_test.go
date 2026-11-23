//go:build e2e

package e2e

import (
	"os"
	"strings"
	"syscall"
	"testing"
)

func TestABindingGoesOnNoMount(t *testing.T) {
	e := startEmu(t)
	workDocs(e)
	d := newDaemon(t, e)
	d.start()
	at := places(t, d)
	d.mustMount("add", "Work/Docs", at+"/docs")
	d.stop()
	d.start()
	if err := os.Mkdir(at+"/tmp", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mount("tmpfs", at+"/tmp", "tmpfs", 0, ""); err != nil {
		t.Fatal(err)
	}
	out, err := d.mountCmd("add", "Work", at+"/tmp")
	if err == nil || !strings.Contains(out, "something is mounted there") {
		t.Fatalf("mount add onto a mount: %v\n%s", err, out)
	}
}
