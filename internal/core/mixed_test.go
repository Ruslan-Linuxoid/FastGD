package core_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

const mixedSize = 2 << 20

func swapAtCut(env *testenv.Env, id string, seed uint64) []byte {
	next := testenv.RandomBytes(mixedSize, seed)
	env.Emu.CutMedia(1, mixedSize/2)
	env.Emu.OnMediaCut(func() {
		env.Emu.OnMediaCut(nil)
		env.Emu.Update(id, func(f *emulator.File) { f.Content = next })
	})
	return next
}

func withoutRevision(t *testing.T, env *testenv.Env) {
	t.Helper()
	if err := env.FS.Store().SetRev(item(t, env, "f.bin").Ino, ""); err != nil {
		t.Fatal(err)
	}
}

func mediaGets(env *testenv.Env) int {
	n := 0
	for _, r := range env.Emu.Requests() {
		if r.Method == http.MethodGet && r.Query.Get("alt") == "media" {
			n++
		}
	}
	return n
}

func TestResumedDownloadKeepsToItsRevision(t *testing.T) {
	t.Parallel()
	env, old := fromDrive(t, mixedSize, 1355)
	rev := item(t, env, "f.bin").Rev
	next := swapAtCut(env, item(t, env, "f.bin").ID, 1356)
	if got := readAll(t, env, "f.bin"); !bytes.Equal(got, old) {
		t.Fatal("the resumed download gives other bytes than the version it started with")
	}
	for _, r := range env.Emu.Requests() {
		if r.Query.Get("alt") == "media" && !strings.HasSuffix(r.Path, "/revisions/"+rev) {
			t.Fatalf("a piece of the file came from %s, not from the revision %s", r.Path, rev)
		}
	}
	poll(t, env)
	if got := readAll(t, env, "f.bin"); !bytes.Equal(got, next) {
		t.Fatal("after the poll a read gives other bytes than the new version")
	}
}

func TestRevisionGoneMidDownloadReadsTheFileAnew(t *testing.T) {
	t.Parallel()
	env, _ := fromDrive(t, mixedSize, 1357)
	it := item(t, env, "f.bin")
	next := testenv.RandomBytes(mixedSize, 1358)
	env.Emu.CutMedia(1, mixedSize/2)
	env.Emu.OnMediaCut(func() {
		env.Emu.OnMediaCut(nil)
		env.Emu.Update(it.ID, func(f *emulator.File) { f.Content = next })
		env.Emu.DeleteRevision(it.ID, it.Rev)
	})
	fh := openFile(t, env, "f.bin")
	_, err := env.FS.Read(context.Background(), fh, 0, make([]byte, mixedSize))
	env.FS.Release(fh)
	if err == nil {
		t.Fatal("scenario not built: the read whose revision went passed")
	}
	if item(t, env, "f.bin").MD5 == md5Of(next) {
		t.Fatal("scenario not built: the item knew the new version before Run read it anew")
	}
	env.FS.ReadGone(context.Background())
	if item(t, env, "f.bin").MD5 != md5Of(next) {
		t.Fatal("after the revision went, the item does not know the new version")
	}
	if got := readAll(t, env, "f.bin"); !bytes.Equal(got, next) {
		t.Fatal("after the revision went, a read gives other bytes than the new version")
	}
}

func TestResumedDownloadOfANewVersionLeavesNoMix(t *testing.T) {
	t.Parallel()
	env, _ := fromDrive(t, mixedSize, 1350)
	withoutRevision(t, env)
	next := swapAtCut(env, item(t, env, "f.bin").ID, 1351)
	readAll(t, env, "f.bin")
	waitUntil(t, func() bool { return item(t, env, "f.bin").MD5 == md5Of(next) })
	if got := readAll(t, env, "f.bin"); !bytes.Equal(got, next) {
		t.Fatal("after the mix was found, a read gives other bytes than the new version")
	}
	if got := readAll(t, env, "f.bin"); !bytes.Equal(got, next) {
		t.Fatal("the new version is not what the cache holds")
	}
}

func TestResumedDownloadOfTheSameVersionAsksNothingMore(t *testing.T) {
	t.Parallel()
	env, content := fromDrive(t, mixedSize, 1352)
	env.Emu.CutMedia(1, mixedSize/2)
	if got := readAll(t, env, "f.bin"); !bytes.Equal(got, content) {
		t.Fatal("the resumed download gives other bytes")
	}
	if n := len(env.Emu.Requests()); n != 2 {
		t.Fatalf("a download cut once went to Drive with %d requests, want the download and its resume", n)
	}
	if err := env.FS.CheckCache(context.Background(), item(t, env, "f.bin").Item); err != nil {
		t.Fatal(err)
	}
	env.Emu.ResetRequests()
	if got := readAll(t, env, "f.bin"); !bytes.Equal(got, content) || mediaGets(env) != 0 {
		t.Fatalf("after the check the file gives other bytes %v or goes to Drive %d times", !bytes.Equal(got, content),
			mediaGets(env))
	}
}

func TestFirstWriteOnAMixedBaseSendsNoMix(t *testing.T) {
	t.Parallel()
	env, _ := fromDrive(t, mixedSize, 1353)
	withoutRevision(t, env)
	next := swapAtCut(env, item(t, env, "f.bin").ID, 1354)
	fh, err := env.FS.Open(context.Background(), env.MustResolve("f.bin").Ino, syscall.O_WRONLY)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := env.FS.Write(context.Background(), fh, 100, []byte("edit")); err != nil {
		t.Fatal(err)
	}
	env.FS.Release(fh)
	uploadAll(t, env)
	want := bytes.Clone(next)
	copy(want[100:], "edit")
	if !bytes.Equal(inDrive(t, env, "f.bin").Content, want) {
		t.Fatal("the edit went to Drive on other bytes than those of the new version")
	}
}

func TestAWriteReadingAnewDuringAPollIsInterrupted(t *testing.T) {
	t.Parallel()
	env, _ := fromDrive(t, mixedSize, 1911)
	withoutRevision(t, env)
	id := item(t, env, "f.bin").ID
	swapAtCut(env, id, 1912)
	fh, err := env.FS.Open(context.Background(), env.MustResolve("f.bin").Ino, syscall.O_WRONLY)
	if err != nil {
		t.Fatal(err)
	}
	defer env.FS.Release(fh)
	release := env.FS.HoldPoll()
	var once sync.Once
	defer once.Do(release)
	env.Emu.ResetRequests()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := env.FS.Write(ctx, fh, 100, []byte("edit"))
		done <- err
	}()

	for deadline := time.Now().Add(10 * time.Second); metaGets(env, id) == 0; time.Sleep(10 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("scenario not built: the write did not read the file anew")
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, syscall.EINTR) {
			t.Fatalf("the interrupted write got %v, want EINTR", err)
		}
	case <-time.After(2 * time.Second):
		once.Do(release)
		t.Logf("once the poll was over the write got %v", <-done)
		t.Fatal("the interrupted write waited for the poll")
	}
}

func metaGets(env *testenv.Env, id string) int {
	n := 0
	for _, r := range env.Emu.Requests() {
		if r.Method == http.MethodGet && strings.HasSuffix(r.Path, "/files/"+id) && r.Query.Get("alt") != "media" {
			n++
		}
	}
	return n
}
