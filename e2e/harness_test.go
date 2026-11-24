//go:build e2e

package e2e

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"net"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/config"
	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"github.com/Ruslan-Linuxoid/FastGD/internal/mounts"
)

const (
	testUID = 4321
	testGID = 8765
)

func daemonBin(t *testing.T) string {
	t.Helper()
	b := os.Getenv("FASTGD_BIN")
	if b == "" {
		t.Fatal("FASTGD_BIN is not set: run the e2e tests with make e2e")
	}
	return b
}

type emu struct {
	*emulator.Emulator

	srv *httptest.Server
}

func startEmu(t *testing.T) *emu {
	t.Helper()
	e := emulator.New()
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)
	return &emu{Emulator: e, srv: srv}
}

type daemon struct {
	t     *testing.T
	emu   *emu
	dir   string
	mnt   string
	cache string
	cmd   *exec.Cmd
	out   *syncBuffer
	done  chan struct{}

	user string

	cacheLimit int64

	dieAfter string

	stopGrace string

	env     []string
	readyIn time.Duration
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func newDaemon(t *testing.T, e *emu) *daemon {
	t.Helper()

	dir := t.TempDir()
	return newDaemonIn(t, e, dir, "/dev/null", filepath.Join(dir, "token.json"))
}

func newDaemonIn(t *testing.T, e *emu, dir, clientID, token string) *daemon {
	t.Helper()

	d := &daemon{t: t, emu: e, dir: dir, mnt: config.MountFolder(dir, emulator.Email), cache: filepath.Join(dir, "cache")}
	cfg := fmt.Sprintf(`mount_in = %q
cache_dir = %q
cache_size = "1G"
client_id_file = %q
token_file = %q
uid = %d
gid = %d
file_mode = "0644"
dir_mode = "0755"
`, d.dir, d.cache, clientID, token, testUID, testGID)
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.cleanup)
	return d
}

func (d *daemon) start() {
	d.t.Helper()
	sock := filepath.Join(d.dir, "notify.sock")
	_ = os.Remove(sock)
	conn, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: sock, Net: "unixgram"})
	if err != nil {
		d.t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := os.Chmod(sock, 0o666); err != nil {
		d.t.Fatal(err)
	}
	ready := make(chan struct{})
	go waitForReady(conn, ready)

	d.out = &syncBuffer{}
	d.cmd = d.command()
	d.cmd.Env = append(d.cmd.Env, "NOTIFY_SOCKET="+sock)
	d.cmd.Stdout = d.out
	d.cmd.Stderr = d.out
	if err := d.cmd.Start(); err != nil {
		d.t.Fatal(err)
	}
	d.done = make(chan struct{})
	go func() {
		_ = d.cmd.Wait()
		close(d.done)
	}()
	select {
	case <-ready:
		if !liveMount(d.mnt) {
			d.t.Fatalf("READY=1 came before the mount answered:\n%s", d.out)
		}
	case <-d.done:
		d.t.Fatalf("daemon exited before it was ready (%v):\n%s", d.cmd.ProcessState, d.out)
	case <-time.After(cmp.Or(d.readyIn, 30*time.Second)):
		d.t.Fatalf("no READY=1 after %v:\n%s", cmp.Or(d.readyIn, 30*time.Second), d.out)
	}
}

func waitForReady(conn *net.UnixConn, ready chan<- struct{}) {
	buf := make([]byte, 4096)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			return
		}
		if strings.Contains(string(buf[:n]), "READY=1") {
			close(ready)
			return
		}
	}
}

func (d *daemon) command() *exec.Cmd {
	cmd := exec.CommandContext(context.Background(), daemonBin(d.t), "run", "--config", filepath.Join(d.dir,
		"config.toml"))
	cmd.Env = append(os.Environ(), d.env...)
	if d.emu != nil {
		cmd.Env = append(cmd.Env, "FASTGD_DRIVE_URL="+d.emu.srv.URL)
	}
	if d.cacheLimit > 0 {
		cmd.Env = append(cmd.Env, fmt.Sprintf("FASTGD_CACHE_LIMIT=%d", d.cacheLimit))
	}
	if d.dieAfter != "" {
		cmd.Env = append(cmd.Env, "FASTGD_DIE_AFTER="+d.dieAfter)
	}
	if d.stopGrace != "" {
		cmd.Env = append(cmd.Env, "FASTGD_STOP_GRACE="+d.stopGrace)
	}
	if d.user != "" {
		cmd.Args = append(cmd.Args, "--user", d.user)
	}
	return cmd
}

func openPath(t *testing.T, dir string) {
	t.Helper()
	for _, p := range []string{filepath.Dir(dir), dir} {
		if err := os.Chmod(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func (d *daemon) stop() {
	d.t.Helper()
	if err := d.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		d.t.Fatal(err)
	}
	select {
	case <-d.done:
	case <-time.After(20 * time.Second):
		d.t.Fatalf("daemon did not exit after SIGTERM:\n%s", d.out)
	}
	if code := d.cmd.ProcessState.ExitCode(); code != 0 {
		d.t.Fatalf("daemon exited with %d:\n%s", code, d.out)
	}
}

func (d *daemon) running() bool {
	if d.done == nil {
		return false
	}
	select {
	case <-d.done:
		return false
	default:
		return true
	}
}

func (d *daemon) cleanup() {
	if d.running() {
		_ = d.cmd.Process.Kill()
		<-d.done
	}
	if mounted(d.mnt) {
		_ = syscall.Unmount(d.mnt, syscall.MNT_DETACH)
	}
	if (d.t.Failed() || os.Getenv("E2E_DAEMON_LOG") != "") && d.out != nil {
		d.t.Logf("daemon log:\n%s", d.out)
	}
}

func (d *daemon) path(p string) string {
	if top, ok := strings.CutPrefix(p, "/"); ok {
		return filepath.Join(d.mnt, top)
	}
	return filepath.Join(d.mnt, meta.MyDriveName, p)
}

func (d *daemon) myDrive() string { return filepath.Join(d.mnt, meta.MyDriveName) }

func mounted(dir string) bool {
	types, err := mounts.At(dir)
	return err == nil && len(types) > 0
}

func liveMount(dir string) bool {
	if !mounted(dir) {
		return false
	}
	_, err := os.ReadDir(dir)
	return err == nil
}
