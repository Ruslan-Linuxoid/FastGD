//go:build e2e

package e2e

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/mounts"
)

func (d *daemon) becomes(name string, uid, gid int) {
	d.t.Helper()
	openPath(d.t, d.dir)
	if err := os.Chown(d.dir, uid, gid); err != nil {
		d.t.Fatal(err)
	}
	d.user = name
}

func (d *daemon) mountCmd(args ...string) (string, error) {
	d.t.Helper()
	all := append([]string{"mount", args[0], "--config", filepath.Join(d.dir, "config.toml")}, args[1:]...)
	out, err := exec.CommandContext(context.Background(), daemonBin(d.t), all...).CombinedOutput()
	return string(out), err
}

func (d *daemon) mustMount(args ...string) string {
	d.t.Helper()
	out, err := d.mountCmd(args...)
	if err != nil {
		d.t.Fatalf("mount %s: %v\n%s\ndaemon log:\n%s", strings.Join(args, " "), err, out, d.out)
	}
	return out
}

func places(t *testing.T, d *daemon) string {
	t.Helper()
	dir := filepath.Join(d.dir, "places")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if d.running() {
			_ = d.cmd.Process.Signal(syscall.SIGTERM)
			select {
			case <-d.done:
			case <-time.After(20 * time.Second):
				_ = d.cmd.Process.Kill()
				<-d.done
			}
		}
		entries, _ := os.ReadDir(dir)
		for _, en := range entries {
			for mounted(filepath.Join(dir, en.Name())) {
				if syscall.Unmount(filepath.Join(dir, en.Name()), syscall.MNT_DETACH) != nil {
					break
				}
			}
		}
	})
	return dir
}

func bound(dir string) bool {
	types, err := mounts.At(dir)
	return err == nil && len(types) > 0 && types[len(types)-1] == "fuse.fastgd"
}

func requireA(t *testing.T, path string) {
	t.Helper()
	if got, err := os.ReadFile(path); err != nil || string(got) != "a" {
		t.Fatalf("%s reads %q, %v; want a", path, got, err)
	}
}

func workDocs(e *emu) string {
	docs := e.Mkdir(e.Mkdir(e.Root(), "Work"), "Docs")
	e.WriteFile(docs, "a.txt", []byte("a"))
	return docs
}

func TestBindingsFollowTheirFolders(t *testing.T) {
	e := startEmu(t)
	docs := workDocs(e)
	d := newDaemon(t, e)
	d.start()
	at := places(t, d)
	if out := d.mustMount("add", "Work/Docs", at+"/docs"); !strings.Contains(out, "next start") {
		t.Fatalf("mount add into an empty list said %q", out)
	}
	d.stop()
	d.start()
	requireA(t, at+"/docs/a.txt")
	if out := d.mustMount("add", "Work", at+"/work"); strings.TrimSpace(out) != "bound" {
		t.Fatalf("mount add said %q", out)
	}
	requireA(t, at+"/work/Docs/a.txt")

	e.Update(docs, func(f *emulator.File) { f.Name = "Papers" })
	waitFor(t, "the list follows the folder", 20*time.Second, func() bool {
		return strings.Contains(d.mustMount("list"), "/Work/Papers")
	})
	requireA(t, at+"/docs/a.txt")

	if err := syscall.Unmount(at+"/docs", syscall.MNT_DETACH); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the binding comes back", 5*time.Second, func() bool { return bound(at + "/docs") })
	requireA(t, at+"/docs/a.txt")

	d.stop()
	if mounted(at+"/docs") || mounted(at+"/work") {
		t.Fatal("a binding outlived the daemon")
	}
	d.start()
	requireA(t, at+"/docs/a.txt")
	requireA(t, at+"/work/Papers/a.txt")
}

func TestTheBindingsOfAKilledDaemonComeBack(t *testing.T) {
	e := startEmu(t)
	workDocs(e)
	d := newDaemon(t, e)
	d.start()
	at := places(t, d)
	d.mustMount("add", "Work/Docs", at+"/docs")
	d.stop()
	d.start()
	if err := d.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-d.done
	d.start()
	requireA(t, at+"/docs/a.txt")
	if types, err := mounts.At(at + "/docs"); err != nil || len(types) != 1 {
		t.Fatalf("the place carries %q, %v", types, err)
	}
}

func TestTheBindingOfAFolderInTheTrashIsIgnored(t *testing.T) {
	e := startEmu(t)
	docs := workDocs(e)
	d := newDaemon(t, e)
	d.start()
	at := places(t, d)
	d.mustMount("add", "Work/Docs", at+"/docs")
	d.stop()
	d.start()
	requireA(t, at+"/docs/a.txt")

	e.Update(docs, func(f *emulator.File) { f.Trashed = true })
	waitFor(t, "the binding is undone", 20*time.Second, func() bool { return !mounted(at + "/docs") })
	if out := d.mustMount("list"); !strings.Contains(out, "ignored: in Drive's trash") {
		t.Fatalf("the list is %q", out)
	}
	e.Update(docs, func(f *emulator.File) { f.Trashed = false })
	d.stop()
	d.start()
	if mounted(at + "/docs") {
		t.Fatal("an ignored binding came back")
	}
	out, err := exec.CommandContext(t.Context(), daemonBin(t), "status", "--config",
		filepath.Join(d.dir, "config.toml")).CombinedOutput()
	if err != nil || !strings.Contains(string(out), at+"/docs is ignored, its folder is in Drive's trash") {
		t.Fatalf("status: %v\n%s", err, out)
	}
}

func TestOnlyTheHelperKeepsRoot(t *testing.T) {
	addDaemonUser(t)
	setFuseConf(t, "user_allow_other\n")
	e := startEmu(t)
	workDocs(e)
	d := newDaemon(t, e)
	d.becomes("fastgd", daemonUID, daemonGID)
	d.start()
	requireUser(t, d.cmd.Process.Pid, daemonUID)
	at := places(t, d)
	d.mustMount("add", "Work/Docs", at+"/docs")
	d.stop()
	d.start()
	requireA(t, at+"/docs/a.txt")
	requireUser(t, d.cmd.Process.Pid, daemonUID)
	kids := childrenOf(t, d.cmd.Process.Pid)
	if len(kids) != 1 || !strings.Contains(cmdline(t, kids[0]), "bind-helper") {
		t.Fatalf("the daemon runs %v", kids)
	}
	requireUser(t, kids[0], 0)
	d.mustMount("add", "Work", at+"/work")
	requireA(t, at+"/work/Docs/a.txt")

	if err := syscall.Kill(kids[0], syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	d.stop()
	if mounted(at+"/docs") || mounted(at+"/work") {
		t.Fatal("a binding outlived the daemon")
	}
}
