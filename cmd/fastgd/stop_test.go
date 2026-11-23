package main

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func writeNew(t *testing.T, env *testenv.Env, name, content string) {
	t.Helper()
	_, fh, err := env.FS.Create(env.MustResolve("").Ino, name, syscall.O_WRONLY)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.FS.Write(context.Background(), fh, 0, []byte(content)); err != nil {
		t.Fatal(err)
	}
	env.FS.Release(fh)
}

func waitFourUploads(t *testing.T, env *testenv.Env) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); env.Emu.UploadsWaiting() != 4; {
		if time.Now().After(deadline) {
			t.Fatal("scenario not built: the four uploads do not take their places")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestStopWithTheUploadsTakenReachesTheDrain(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	for _, name := range []string{"long0.bin", "long1.bin", "long2.bin", "long3.bin"} {
		writeNew(t, env, name, "long upload")
	}
	env.Clock.Advance(time.Minute)

	release := sync.OnceFunc(env.Emu.HoldUploads())
	defer release()
	held := make(chan struct{})
	go func() {
		_, _ = env.FS.SyncNow(context.Background())
		close(held)
	}()
	waitFourUploads(t, env)
	writeNew(t, env, "late.txt", "late")
	env.Clock.Advance(time.Minute)
	path := filepath.Join(t.TempDir(), "control.sock")
	stop, err := serveControl(path, env.FS, nil, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 1)
	go func() {
		line, _ := ask(path, "sync")
		got <- line
	}()

	time.Sleep(100 * time.Millisecond)

	stopped, drained := make(chan struct{}), make(chan struct{})
	go func() {
		stop()
		close(stopped)
		env.FS.Drain(time.Second)
		close(drained)
	}()
	for _, step := range []struct {
		what string
		done chan struct{}
	}{{"the stop of the socket", stopped}, {"the drain", drained}} {
		select {
		case <-step.done:
		case <-time.After(10 * time.Second):
			t.Fatalf("with the uploads taken, %s does not return", step.what)
		}
	}
	if line := <-got; !strings.HasPrefix(line, "error ") {
		t.Fatalf("a sync that the stop called off answered %q, want an error", line)
	}
	release()
	<-held
}
