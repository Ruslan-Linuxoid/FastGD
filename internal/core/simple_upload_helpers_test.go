package core_test

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

const overtakeSize = 8 << 20

func hookOvertake(t *testing.T, env *testenv.Env, patch []byte) (hooked, fired *atomic.Bool) {
	t.Helper()
	hooked, fired = new(atomic.Bool), new(atomic.Bool)
	tr := &hookTransport{
		next: env.Srv.Client().Transport,
		match: func(r *http.Request) bool {
			return hooked.Load() && strings.HasPrefix(r.URL.Path, "/upload/") && r.URL.Query().Get("uploadType") != "resumable"
		},
		n: 1 << 20,
	}
	tr.fn = func() {
		fired.Store(true)
		fh, err := env.FS.Open(context.Background(), env.MustResolve("f.bin").Ino, syscall.O_RDWR)
		if err != nil {
			t.Errorf("open: %v", err)
			return
		}

		if n, err := env.FS.Write(context.Background(), fh, 0, patch); err != nil || n != len(patch) {
			t.Errorf("write head: %d %v", n, err)
		}
		tail := overtakeSize - int64(len(patch))
		if n, err := env.FS.Write(context.Background(), fh, tail, patch); err != nil || n != len(patch) {
			t.Errorf("write tail: %d %v", n, err)
		}
		env.FS.Release(fh)
	}
	cl, err := drive.New(context.Background(), drive.Options{BaseURL: env.Srv.URL,
		HTTPClient: &http.Client{Transport: tr}, Limits: testenv.Limits()})
	if err != nil {
		t.Fatal(err)
	}
	env.Drive = cl
	return hooked, fired
}

func makeFileDueAgain(t *testing.T, env *testenv.Env, content, want []byte) (before []byte, version int64) {
	t.Helper()
	uploadAll(t, env)
	if got, _ := driveContent(env, "f.bin"); !bytes.Equal(got, content) {
		t.Fatal("scenario not built: the first upload differs")
	}

	fh := openWriter(t, env, "f.bin", 0)
	write(t, env, fh, overtakeSize/2, []byte("x"))
	env.FS.Release(fh)
	before = append([]byte(nil), content...)
	content[overtakeSize/2] = 'x'
	want[overtakeSize/2] = 'x'

	return before, inDrive(t, env, "f.bin").Version
}

func requireWriteDuringSimpleUpload(t *testing.T, update bool) {
	t.Helper()
	env := testenv.New(t)
	content := testenv.RandomBytes(overtakeSize, 200)
	patch := testenv.RandomBytes(4<<10, 201)
	want := append([]byte(nil), content...)
	copy(want, patch)
	copy(want[overtakeSize-len(patch):], patch)
	hooked, fired := hookOvertake(t, env, patch)
	env.Start()
	writeFile(t, env, "", "f.bin", content)

	var before []byte
	var version int64
	if update {
		before, version = makeFileDueAgain(t, env, content, want)
	}
	hooked.Store(true)
	env.Clock.Advance(20 * time.Minute)
	env.FS.SyncOnce(context.Background())
	if !fired.Load() {
		t.Fatal("scenario not built: the hook never fired")
	}
	requireOvertakenUploadLeftDriveAsItWas(t, env, update, before, version)
	hooked.Store(false)
	uploadAll(t, env)
	if got, _ := driveContent(env, "f.bin"); !bytes.Equal(got, want) {
		t.Fatal("the written file went up different")
	}
}

func requireOvertakenUploadLeftDriveAsItWas(t *testing.T, env *testenv.Env, update bool, before []byte, version int64) {
	t.Helper()
	got, ok := driveContent(env, "f.bin")
	if ok != update {
		t.Fatalf("Drive holds the file: %v, want %v", ok, update)
	}
	if ok && !bytes.Equal(got, before) {
		t.Fatal("Drive holds a version that never was here")
	}
	if ok && inDrive(t, env, "f.bin").Version != version {
		t.Fatal("Drive kept an upload that a write overtook")
	}
	if logs := env.Logs("upload failed"); len(logs) != 0 {
		t.Fatalf("the overtaken upload counts as failed: %q", logs)
	}
}
