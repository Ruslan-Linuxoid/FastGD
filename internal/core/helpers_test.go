package core_test

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func createFile(t *testing.T, env *testenv.Env, dir, name string) (uint64, uint64) {
	t.Helper()
	a, fh, err := env.FS.Create(env.MustResolve(dir).Ino, name, syscall.O_WRONLY)
	if err != nil {
		t.Fatalf("create %s/%s: %v", dir, name, err)
	}
	return a.Ino, fh
}

func mkdir(t *testing.T, env *testenv.Env, dir, name string) {
	t.Helper()
	if _, err := env.FS.Mkdir(env.MustResolve(dir).Ino, name); err != nil {
		t.Fatalf("mkdir %s/%s: %v", dir, name, err)
	}
}

func write(t *testing.T, env *testenv.Env, fh uint64, off int64, data []byte) {
	t.Helper()
	if n, err := env.FS.Write(context.Background(), fh, off, data); err != nil || n != len(data) {
		t.Fatalf("write %d at %d: %d, %v", len(data), off, n, err)
	}
}

func writeFile(t *testing.T, env *testenv.Env, dir, name string, content []byte) {
	t.Helper()
	_, fh := createFile(t, env, dir, name)
	write(t, env, fh, 0, content)
	env.FS.Release(fh)
}

type tried struct {
	meta.Item

	Attempts  int
	NextTry   time.Time
	LastError string
}

func item(t *testing.T, env *testenv.Env, path string) tried {
	t.Helper()
	it, err := env.FS.Store().ByIno(meta.Ino(env.MustResolve(path).Ino))
	if err != nil {
		t.Fatal(err)
	}
	ops, err := env.FS.Store().Ops(it.Ino)
	if err != nil {
		t.Fatal(err)
	}
	out := tried{Item: it}
	if len(ops) > 0 {
		out.Attempts, out.NextTry, out.LastError = ops[0].Attempts, ops[0].NextTry, ops[0].LastError
	}
	return out
}

func uploadAll(t *testing.T, env *testenv.Env) {
	t.Helper()
	for i := 0; ; i++ {
		if i > 30 {
			t.Fatal("uploads never settle")
		}
		env.Clock.Advance(20 * time.Minute)
		if env.FS.SyncOnce(context.Background()) == 0 {
			return
		}
	}
}

func inDrive(t *testing.T, env *testenv.Env, path ...string) emulator.File {
	t.Helper()
	found, ok := env.Emu.ByPath(path...)
	if !ok {
		t.Fatalf("Drive has not exactly one item under %q", path)
	}
	return found
}

func md5Of(b []byte) string {
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}

func waitUntil(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := wallNow().Add(5 * time.Second)
	for !cond() {
		if wallNow().After(deadline) {
			t.Fatal("condition not reached within 5s")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func wallNow() time.Time {
	var tv syscall.Timeval
	if err := syscall.Gettimeofday(&tv); err != nil {
		panic(err)
	}
	return time.Unix(tv.Unix())
}

func fmtInode(ino uint64, content bool) string { return fmt.Sprintf("%d content=%v", ino, content) }

func within(t *testing.T, what string, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatalf("%s waits for a lock", what)
	}
}

func openFile(t *testing.T, env *testenv.Env, path string) uint64 {
	t.Helper()
	fh, err := env.FS.Open(context.Background(), env.MustResolve(path).Ino, syscall.O_RDONLY)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { env.FS.Release(fh) })
	return fh
}

func mustRead(t *testing.T, env *testenv.Env, fh uint64, off int64, n int) []byte {
	t.Helper()
	buf := make([]byte, n)
	got, err := env.FS.Read(context.Background(), fh, off, buf)
	if err != nil {
		t.Fatalf("read %d at %d: %v", n, off, err)
	}
	return buf[:got]
}

type recorder struct {
	mu      sync.Mutex
	entries []string
	inodes  []string
}

func (r *recorder) Entry(parent uint64, name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, fmt.Sprintf("%d/%s", parent, name))
}

func (r *recorder) Attrs(ino uint64) { r.inode(ino, false) }

func (r *recorder) Content(ino uint64) { r.inode(ino, true) }

func (r *recorder) inode(ino uint64, content bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inodes = append(r.inodes, fmtInode(ino, content))
}

func poll(t *testing.T, env *testenv.Env) {
	t.Helper()
	if err := env.FS.PollChanges(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func readAll(t *testing.T, env *testenv.Env, path string) []byte {
	t.Helper()
	a := env.MustResolve(path)
	fh := openFile(t, env, path)
	return mustRead(t, env, fh, 0, int(a.Size))
}

func rename(t *testing.T, env *testenv.Env, oldDir, oldName, newDir, newName string) {
	t.Helper()
	err := env.FS.Rename(env.MustResolve(oldDir).Ino, oldName, env.MustResolve(newDir).Ino, newName, 0)
	if err != nil {
		t.Fatalf("rename %s/%s to %s/%s: %v", oldDir, oldName, newDir, newName, err)
	}
}

func uploads(env *testenv.Env) (content, metadata int) {
	for _, r := range env.Emu.Requests() {
		switch {
		case strings.HasPrefix(r.Path, "/upload/"):
			content++
		case r.Method == http.MethodPatch || (r.Method == http.MethodPost && r.Path == "/drive/v3/files"):
			metadata++
		}
	}
	return content, metadata
}

func heard(rec *recorder, parent uint64, names ...string) bool {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, name := range names {
		if !slices.Contains(rec.entries, fmt.Sprintf("%d/%s", parent, name)) {
			return false
		}
	}
	return true
}

func toldOf(rec *recorder, ino uint64) bool {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return slices.Contains(rec.inodes, fmtInode(ino, false))
}

func openWriter(t *testing.T, env *testenv.Env, path string, flags uint32) uint64 {
	t.Helper()
	fh, err := env.FS.Open(context.Background(), env.MustResolve(path).Ino, syscall.O_RDWR|flags)
	if err != nil {
		t.Fatalf("open %s for writing: %v", path, err)
	}
	return fh
}

func rounds(env *testenv.Env, n int) {
	for range n {
		env.Clock.Advance(20 * time.Minute)
		env.FS.SyncOnce(context.Background())
	}
}
