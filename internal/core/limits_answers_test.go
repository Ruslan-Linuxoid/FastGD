package core_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestFolderLimitsGiveENOSPC(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Tune = func(o *core.Options) { o.MaxChildren, o.MaxDepth = 3, 3 }
	env.Start()
	mkdir(t, env, "", "full")
	requireFullFolderRefuses(t, env)
	requireDepthLimitRefuses(t, env)
	requireRefusedItemsStayOutOfDrive(t, env)
}

func TestAnswerWithSomethingElseForAFileIDIsRefused(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Emu.GiveIDs("ab/../../../x")
	var bad *drive.BadIDError
	if ids, err := env.Drive.GenerateIDs(context.Background(), 3); !errors.As(err, &bad) {
		t.Fatalf("generateIds with a path for an id gave %q, %v", ids, err)
	}
	env.Emu.Put(emulator.File{ID: "../outside", Name: "evil.txt", Parents: []string{env.Emu.Root()}})
	if _, err := env.Open(); !errors.As(err, &bad) {
		t.Fatalf("a listing with a path for an id ended with %v", err)
	}
}

func TestSessionAddressStaysOutOfErrors(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Emu.SetOffline(true)
	session := env.Srv.URL + "/upload/drive/v3/files?uploadType=resumable&upload_id=SECRET123"
	_, err := env.Drive.UploadStatus(context.Background(), session, 10)
	if err == nil || strings.Contains(err.Error(), "SECRET123") || strings.Contains(err.Error(), "upload_id") {
		t.Fatalf("the status question of a session that cannot be reached ended with %v", err)
	}
	if !drive.IsNetwork(err) {
		t.Fatalf("the error is not one of the network: %v", err)
	}
	_, err = env.Drive.SendChunk(context.Background(), session, 0, 4, 10, strings.NewReader("data"))
	if err == nil || strings.Contains(err.Error(), "SECRET123") {
		t.Fatalf("the chunk of a session that cannot be reached ended with %v", err)
	}
}

func requireFullFolderRefuses(t *testing.T, env *testenv.Env) {
	t.Helper()
	for _, name := range []string{"a", "b", "c"} {
		writeFile(t, env, "full", name, []byte(name))
	}
	env.Emu.ResetRequests()
	full := env.MustResolve("full").Ino
	if _, _, err := env.FS.Create(full, "d", syscall.O_WRONLY); err != syscall.ENOSPC {
		t.Fatalf("a create in a full folder got %v, want ENOSPC", err)
	}
	if _, err := env.FS.Mkdir(full, "d"); err != syscall.ENOSPC {
		t.Fatalf("a mkdir in a full folder got %v, want ENOSPC", err)
	}
	writeFile(t, env, "", "loose", []byte("x"))
	if err := env.FS.Rename(env.MustResolve("").Ino, "loose", full, "loose", 0); err != syscall.ENOSPC {
		t.Fatalf("a move into a full folder got %v, want ENOSPC", err)
	}
	if len(env.Logs("holds no more items")) == 0 {
		t.Fatal("the reason is not in the log")
	}
}

func requireDepthLimitRefuses(t *testing.T, env *testenv.Env) {
	t.Helper()
	mkdir(t, env, "", "x")
	mkdir(t, env, "x", "y")
	mkdir(t, env, "x/y", "z")
	if _, err := env.FS.Mkdir(env.MustResolve("x/y/z").Ino, "deep"); err != syscall.ENOSPC {
		t.Fatalf("a folder past the depth got %v, want ENOSPC", err)
	}
	mkdir(t, env, "", "p")
	mkdir(t, env, "p", "q")
	if err := env.FS.Rename(env.MustResolve("").Ino, "p", env.MustResolve("x/y").Ino, "p", 0); err != syscall.ENOSPC {
		t.Fatalf("a move of a folder tree past the depth got %v, want ENOSPC", err)
	}
}

func requireRefusedItemsStayOutOfDrive(t *testing.T, env *testenv.Env) {
	t.Helper()
	uploadAll(t, env)
	for _, f := range env.Emu.All() {
		if f.Name == "d" || f.Name == "deep" {
			t.Fatalf("the refused item %q went to Drive", f.Name)
		}
	}
	if _, ok := env.Emu.ByPath("full", "loose"); ok {
		t.Fatal("the file whose move into the full folder was refused lies there in Drive")
	}

	inDrive(t, env, "p", "q")
	if f, _ := env.Emu.Get(item(t, env, "p").ID); len(f.Parents) != 1 || f.Parents[0] != env.Emu.Root() {
		t.Fatalf("the folder whose move was refused lies under %v in Drive", f.Parents)
	}
}

func TestATimeOutOfRangeTakesTheBound(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	far := time.Date(60056, 5, 28, 5, 36, 10, 0, time.UTC)
	env.Emu.Put(emulator.File{ID: "farfuture1", Name: "far.txt", Parents: []string{env.Emu.Root()}, Modified: far,
		Content: []byte("far")})
	env.Start()
	a, err := env.Resolve("far.txt")
	if err != nil {
		t.Fatalf("a file of the year 60056 is not in the mount: %v", err)
	}
	if want := time.Unix(0, math.MaxInt64); !a.Mtime.Equal(want) {
		t.Fatalf("the file of the year 60056 shows the time %v, want the bound %v", a.Mtime, want)
	}
}
