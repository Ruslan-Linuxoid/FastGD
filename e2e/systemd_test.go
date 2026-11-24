//go:build e2e

package e2e

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestReadyComesAfterTheMountAnswers(t *testing.T) {
	e := startEmu(t)
	e.Mkdir(e.Root(), "Photos")
	e.WriteFile(e.Root(), "a.txt", []byte("a"))
	d := newDaemon(t, e)
	d.start()
	if got := dirNames(t, d.myDrive()); !slices.Equal(got, []string{"Photos", "a.txt"}) {
		t.Fatalf("root lists %q right after READY=1", got)
	}
}

func TestOtherUsersReadTheMount(t *testing.T) {
	e := startEmu(t)
	dir := e.Mkdir(e.Root(), "shared")
	e.WriteFile(dir, "note.txt", []byte("for everyone"))
	d := newDaemon(t, e)
	openPath(t, d.dir)
	d.start()
	requireNobodyReads(t, d, "shared", "note.txt", "for everyone")
}

const (
	nobodyUID = 65534
	nobodyGID = 65534

	daemonUID = 2000
	daemonGID = 2000
)

func requireNobodyReads(t *testing.T, d *daemon, dir, name, content string) {
	t.Helper()
	asNobody := func(prog string, args ...string) []byte {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), prog, args...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: nobodyUID, Gid: nobodyGID}}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s %s as nobody: %v\n%s", prog, strings.Join(args, " "), err, out)
		}
		return out
	}
	if out := asNobody("ls", d.path(dir)); strings.TrimSpace(string(out)) != name {
		t.Fatalf("ls as nobody: %q", out)
	}
	if out := asNobody("cat", d.path(dir+"/"+name)); !bytes.Equal(out, []byte(content)) {
		t.Fatalf("cat as nobody: %q", out)
	}
}

func setFuseConf(t *testing.T, content string) {
	t.Helper()
	old, err := os.ReadFile("/etc/fuse.conf")
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	if err := os.WriteFile("/etc/fuse.conf", []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.WriteFile("/etc/fuse.conf", old, 0o644) })
}

func TestUnprivilegedDaemon(t *testing.T) {
	e := startEmu(t)
	dir := e.Mkdir(e.Root(), "shared")
	e.WriteFile(dir, "note.txt", []byte("for everyone"))
	addDaemonUser(t)
	d := newDaemon(t, e)
	d.becomes("fastgd", daemonUID, daemonGID)

	setFuseConf(t, "")

	cmd := d.command()
	timer := time.AfterFunc(30*time.Second, func() { _ = cmd.Process.Kill() })
	out, err := cmd.CombinedOutput()
	timer.Stop()
	if err == nil || !strings.Contains(string(out), "user_allow_other") {
		t.Fatalf("without user_allow_other the daemon gave %v:\n%s", err, out)
	}

	setFuseConf(t, "user_allow_other\n")
	d.start()
	requireUser(t, d.cmd.Process.Pid, daemonUID)
	if kids := childrenOf(t, d.cmd.Process.Pid); len(kids) != 0 {
		t.Fatalf("with an empty list the daemon runs %v", kids)
	}
	requireNobodyReads(t, d, "shared", "note.txt", "for everyone")
	d.stop()
	requireCleanMountPoint(t, d)

	d.start()
	if err := d.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-d.done
	d.start()
	requireNobodyReads(t, d, "shared", "note.txt", "for everyone")
	d.stop()
	requireCleanMountPoint(t, d)
}

func addDaemonUser(t *testing.T) {
	t.Helper()

	if err := exec.CommandContext(t.Context(), "id", "fastgd").Run(); err != nil {
		for _, cmd := range [][]string{
			{"groupadd", "--system", "--gid", strconv.Itoa(daemonGID), "fastgd"},
			{"useradd", "--system", "--no-create-home", "--uid", strconv.Itoa(daemonUID), "--gid", strconv.Itoa(daemonGID),
				"fastgd"},
		} {
			if out, err := exec.CommandContext(t.Context(), cmd[0], cmd[1:]...).CombinedOutput(); err != nil {
				t.Fatalf("%s: %v\n%s", cmd[0], err, out)
			}
		}
	}
}

func TestMountIsTheOwnersAloneUnlessTheConfigOpensIt(t *testing.T) {
	e := startEmu(t)
	dir := e.Mkdir(e.Root(), "private")
	e.WriteFile(dir, "note.txt", []byte("for the owner"))
	d := newDaemon(t, e)
	writeConfigWithNoModes(t, d)
	openPath(t, d.dir)
	d.start()
	note := d.path("private/note.txt")
	if file, folder := modesOf(t, note, d.path("private")); file != 0o600 || folder != 0o700 {
		t.Fatalf("without modes in the config a file shows %o and a folder %o, want 0600 and 0700", file, folder)
	}
	if err := os.Chmod(note, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if file, _ := modesOf(t, note, d.path("private")); file != 0o600 {
		t.Fatalf("after chmod the file shows %o, want 0600 still", file)
	}
	requireAnotherUserCanDoNothing(t, d, note)
	if got, err := os.ReadFile(note); err != nil || string(got) != "for the owner" {
		t.Fatalf("after the other user's tries the file reads %q, %v", got, err)
	}
}

func writeConfigWithNoModes(t *testing.T, d *daemon) {
	t.Helper()
	path := filepath.Join(d.dir, "config.toml")
	cfg, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for line := range strings.SplitSeq(string(cfg), "\n") {
		if !strings.HasPrefix(line, "file_mode") && !strings.HasPrefix(line, "dir_mode") {
			kept = append(kept, line)
		}
	}
	if err := os.WriteFile(path, []byte(strings.Join(kept, "\n")), 0o644); err != nil {
		t.Fatal(err)
	}
}

func modesOf(t *testing.T, note, folderPath string) (file, folder os.FileMode) {
	t.Helper()
	fi, err := os.Stat(note)
	if err != nil {
		t.Fatal(err)
	}
	di, err := os.Stat(folderPath)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm(), di.Mode().Perm()
}

func requireAnotherUserCanDoNothing(t *testing.T, d *daemon, note string) {
	t.Helper()
	for _, args := range [][]string{
		{"cat", note},
		{"ls", d.path("private")},
		{"sh", "-c", `echo x > "$0"`, note},
		{"rm", note},
		{"touch", d.path("private/new.txt")},
	} {
		cmd := exec.CommandContext(t.Context(), args[0], args[1:]...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: nobodyUID, Gid: nobodyGID}}
		if out, err := cmd.CombinedOutput(); err == nil {
			t.Fatalf("%s as another user went through: %s", strings.Join(args, " "), out)
		}
	}
}
