package core_test

import (
	"bytes"
	"context"
	"slices"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestParentsGoBeforeChildren(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	mkdir(t, env, "", "a")
	mkdir(t, env, "a", "b")
	mkdir(t, env, "a/b", "c")
	writeFile(t, env, "a/b/c", "deep.txt", []byte("d"))
	writeFile(t, env, "", "top.txt", []byte("t"))
	env.Clock.Advance(time.Minute)

	env.FS.SyncOnce(context.Background())
	kids := env.Emu.Children(env.Emu.Root())
	names := make([]string, 0, len(kids))
	for _, f := range kids {
		names = append(names, f.Name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"a", "top.txt"}) {
		t.Fatalf("after round one the root holds %q", names)
	}
	uploadAll(t, env)
	if f := inDrive(t, env, "a", "b", "c", "deep.txt"); string(f.Content) != "d" {
		t.Fatalf("deep.txt has %q", f.Content)
	}
}

func retryBadItemAtItsTime(t *testing.T, env *testenv.Env, attempt int) time.Duration {
	t.Helper()
	it := item(t, env, "bad.txt")
	if it.Attempts != attempt || it.LastError == "" {
		t.Fatalf("attempt %d: stored attempts %d, error %q", attempt, it.Attempts, it.LastError)
	}
	wait := it.NextTry.Sub(env.Clock.Now())

	env.Clock.Advance(wait - time.Millisecond)
	if env.FS.SyncOnce(context.Background()) != 0 {
		t.Fatalf("attempt %d retried before its backoff ended", attempt)
	}
	env.Clock.Advance(time.Millisecond)
	env.FS.SyncOnce(context.Background())
	return wait
}

func requireBackoffsGrowToTheCap(t *testing.T, waits []time.Duration) {
	t.Helper()
	if w := waits[0]; w < 5*time.Second || w > 6*time.Second {
		t.Fatalf("first backoff %v, want 5s to 6s", w)
	}
	for i := 1; i < len(waits); i++ {
		if waits[i] > 15*time.Minute {
			t.Fatalf("backoff %d is %v, over the 15 minute cap", i, waits[i])
		}
	}
	if last := waits[len(waits)-1]; last < 12*time.Minute {
		t.Fatalf("backoff after 12 attempts is %v, want about 15 minutes", last)
	}
}

func TestFailingItemBacksOffAlone(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()

	env.Emu.SetUploadFault(func(name string) int {
		if name == "bad.txt" {
			return 408
		}
		return 0
	})
	writeFile(t, env, "", "bad.txt", []byte("b"))
	writeFile(t, env, "", "good.txt", []byte("g"))
	env.Clock.Advance(time.Minute)
	env.FS.SyncOnce(context.Background())
	inDrive(t, env, "good.txt")

	var waits []time.Duration
	for attempt := 1; attempt <= 12; attempt++ {
		waits = append(waits, retryBadItemAtItsTime(t, env, attempt))
	}
	requireBackoffsGrowToTheCap(t, waits)
}

func TestFourUploadsAtOnce(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	for _, name := range []string{"1", "2", "3", "4", "5", "6"} {
		writeFile(t, env, "", name, []byte(name))
	}
	env.Clock.Advance(time.Minute)
	release := env.Emu.HoldUploads()
	done := make(chan struct{})
	go func() {
		env.FS.SyncOnce(context.Background())
		close(done)
	}()
	waitUntil(t, func() bool { return env.Emu.UploadsWaiting() == 4 })
	release()
	<-done

	if n := env.Emu.MaxUploadsAtOnce(); n != 4 {
		t.Fatalf("%d uploads at once, want 4", n)
	}
	uploadAll(t, env)
	if n := len(env.Emu.Children(env.Emu.Root())); n != 6 {
		t.Fatalf("%d files in Drive, want 6", n)
	}
}

func TestOwnUploadComingBackIsNotForeign(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	content := testenv.RandomBytes(3*big/2, 31)
	writeFile(t, env, "", "f.bin", content)
	uploadAll(t, env)
	rec := &recorder{}
	env.FS.SetNotifier(rec)
	ino := env.MustResolve("f.bin").Ino
	poll(t, env)
	for _, n := range rec.inodes {
		if n == fmtInode(ino, true) {
			t.Fatalf("the daemon's own upload dropped the file's pages: %q", rec.inodes)
		}
	}
	env.Emu.SetOffline(true)
	if got := readAll(t, env, "f.bin"); !bytes.Equal(got, content) {
		t.Fatal("the daemon's own upload dropped the cached content")
	}
}

func TestOwnUploadComingBackKeepsLaterWrites(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	_, fh := createFile(t, env, "", "f.txt")
	write(t, env, fh, 0, []byte("v1"))
	env.Clock.Advance(5*time.Minute + time.Second)
	env.FS.SyncOnce(context.Background())

	write(t, env, fh, 2, []byte(" and v2"))
	poll(t, env)
	if got := readAll(t, env, "f.txt"); string(got) != "v1 and v2" {
		t.Fatalf("the report of the earlier upload replaced local writes: %q", got)
	}
	if it := item(t, env, "f.txt"); !it.Dirty {
		t.Fatal("the report of the earlier upload made the file clean")
	}
	env.FS.Release(fh)
	uploadAll(t, env)
	if f := inDrive(t, env, "f.txt"); string(f.Content) != "v1 and v2" {
		t.Fatalf("Drive has %q", f.Content)
	}
}

func TestBackoffVaries(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	env.Emu.SetUploadFault(func(string) int { return 408 })
	for _, name := range []string{"a", "b", "c", "d"} {
		writeFile(t, env, "", name, []byte(name))
	}
	env.Clock.Advance(time.Minute)
	env.FS.SyncOnce(context.Background())
	waits := map[time.Duration]bool{}
	for _, name := range []string{"a", "b", "c", "d"} {
		w := item(t, env, name).NextTry.Sub(env.Clock.Now())
		if w < 5*time.Second || w > 15*time.Minute {
			t.Fatalf("first backoff %v is outside 5s..15m", w)
		}
		waits[w] = true
	}
	if len(waits) < 2 {
		t.Fatal("four items got the same backoff: no spread")
	}
}
