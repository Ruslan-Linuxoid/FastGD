package core_test

import (
	"bytes"
	"context"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

const notNeededSize = 3<<20 + 100

func writeOnce(t *testing.T, env *testenv.Env, flags uint32, off int64, data []byte) {
	t.Helper()
	fh := openWriter(t, env, "f.bin", flags)
	write(t, env, fh, off, data)
	env.FS.Release(fh)
}

func requireSentWithoutBase(t *testing.T, env *testenv.Env, want []byte) {
	t.Helper()
	env.FS.FillBases(context.Background())
	if got := env.Emu.Requests(); len(got) != 0 {
		t.Fatalf("Drive was asked %d times for a version that is not needed, the first %+v", len(got), got[0])
	}
	requireWholeOffline(t, env, want)
	uploadAll(t, env)
	requireRanges(t, env)
	if f := inDrive(t, env, "f.bin"); !bytes.Equal(f.Content, want) {
		t.Fatal("Drive holds other content than the file has here")
	}
}

func startWithBase(t *testing.T, seed uint64) (*testenv.Env, []byte) {
	t.Helper()
	env := testenv.New(t)
	content := testenv.RandomBytes(notNeededSize, seed)
	env.Emu.WriteFile(env.Emu.Root(), "f.bin", content)
	env.Start()
	env.Emu.ResetRequests()
	return env, content
}

func TestBaseThatIsNotNeededIsNotFetched(t *testing.T) {
	t.Parallel()
	const size = notNeededSize
	sent := requireSentWithoutBase
	start := startWithBase
	t.Run("one write covers the file", func(t *testing.T) {
		t.Parallel()
		env, _ := start(t, 840)
		want := testenv.RandomBytes(size, 841)
		writeOnce(t, env, 0, 0, want)
		sent(t, env, want)
	})
	t.Run("one write covers the file and more", func(t *testing.T) {
		t.Parallel()
		env, _ := start(t, 842)
		want := testenv.RandomBytes(size+5000, 843)
		writeOnce(t, env, 0, 0, want)
		sent(t, env, want)
	})
	t.Run("a new file is saved over it", func(t *testing.T) {
		t.Parallel()
		env, _ := start(t, 844)
		want := testenv.RandomBytes(9000, 845)
		writeFile(t, env, "", "f.tmp", want)
		rename(t, env, "", "f.tmp", "", "f.bin")
		sent(t, env, want)
	})
	t.Run("truncate to nothing", func(t *testing.T) {
		t.Parallel()
		env, _ := start(t, 846)
		if err := env.FS.Truncate(context.Background(), env.MustResolve("f.bin").Ino, 0); err != nil {
			t.Fatal(err)
		}
		writeOnce(t, env, 0, 2, []byte("new"))
		sent(t, env, []byte("\x00\x00new"))
	})
	t.Run("opened with O_TRUNC", func(t *testing.T) {
		t.Parallel()
		env, _ := start(t, 847)
		writeOnce(t, env, syscall.O_TRUNC, 0, []byte("new"))
		sent(t, env, []byte("new"))
	})
	t.Run("the cache holds all of it", func(t *testing.T) {
		t.Parallel()
		env, want := start(t, 848)
		if got := readAll(t, env, "f.bin"); !bytes.Equal(got, want) {
			t.Fatal("scenario not built: the file reads different")
		}
		waitUntil(t, func() bool { return cached(t, env, "f.bin") == size })
		env.Emu.ResetRequests()
		patch := testenv.RandomBytes(5000, 849)
		fh := openWriter(t, env, "f.bin", 0)
		write(t, env, fh, 70000, patch)
		copy(want[70000:], patch)
		env.FS.Release(fh)
		sent(t, env, want)
	})
}

func TestFirstWriteWaitsForTheBase(t *testing.T) {
	testenv.InBubble(t, func(t *testing.T, env *testenv.Env) {
		content := startWith(env, 2<<20, 850)
		env.Emu.SetMediaDelay(time.Second)
		fh := openWriter(t, env, "f.bin", 0)
		patch := testenv.RandomBytes(5000, 851)
		start := time.Now()
		done := make(chan error, 1)
		go func() {
			_, err := env.FS.Write(context.Background(), fh, 1<<20+100, patch)
			done <- err
		}()
		synctest.Wait()
		select {
		case err := <-done:
			t.Fatalf("the write answered before its base came: %v", err)
		default:
		}
		if it := item(t, env, "f.bin"); it.Dirty {
			t.Fatal("the file is changed here before its base came")
		}
		if _, err := env.Resolve("f.bin"); err != nil {
			t.Fatal(err)
		}
		if names := env.Names(""); len(names) != 1 {
			t.Fatalf("the root lists %q while the write waits", names)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if took := time.Since(start); took < time.Second {
			t.Fatalf("the write answered after %v, the download takes a second", took)
		}
		copy(content[1<<20+100:], patch)
		env.Emu.SetMediaDelay(0)
		env.Emu.ResetRequests()

		env.FS.FillBases(context.Background())
		if got := env.Emu.Requests(); len(got) != 0 {
			t.Fatalf("after the write Drive was asked %d times, the first %+v", len(got), got[0])
		}
		requireWholeOffline(t, env, content)
		env.FS.Release(fh)
		uploadAll(t, env)
		if f := inDrive(t, env, "f.bin"); md5Of(f.Content) != md5Of(content) {
			t.Fatal("Drive holds other bytes than the edited file")
		}
	})
}

func TestFirstWriteFailsWithoutItsBase(t *testing.T) {
	t.Parallel()
	for name, cut := range map[string]func(env *testenv.Env, id string){
		"no network":       func(env *testenv.Env, _ string) { env.Emu.SetOffline(true) },
		"deleted for good": func(env *testenv.Env, id string) { env.Emu.Delete(id) },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			env, content := fromDrive(t, 256<<10, 852)
			before := item(t, env, "f.bin")
			fh := openWriter(t, env, "f.bin", 0)
			defer env.FS.Release(fh)
			cut(env, before.ID)
			start := time.Now()
			if _, err := env.FS.Write(context.Background(), fh, 8192, make([]byte, 4096)); err != syscall.EIO {
				t.Fatalf("the write got %v, want EIO", err)
			}
			if took := time.Since(start); took > 30*time.Second {
				t.Fatalf("the write failed after %v", took)
			}
			it := item(t, env, "f.bin")
			if it.Dirty || it.Base != 0 || it.Size != before.Size || it.MD5 != before.MD5 {
				t.Fatalf("after the write that failed the file is changed here %v, base %d, size %d", it.Dirty, it.Base, it.Size)
			}
			if name == "no network" {
				env.Emu.SetOffline(false)
				if got := readAll(t, env, "f.bin"); !bytes.Equal(got, content) {
					t.Fatal("the file does not read as its Drive version")
				}
			}
		})
	}
}

func TestMarkOfAFileThatNeedsItsBaseNoMoreIsTakenOff(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	editedFromDrive(t, env, 256<<10, 8192, 395)
	id := item(t, env, "f.bin").ID
	base := inDrive(t, env, "f.bin").HeadRevision
	olderMark(t, env, id, base)
	marked := func() bool {
		for _, r := range env.Emu.Revisions(id) {
			if r.ID == base {
				return r.Keep
			}
		}
		t.Fatal("Drive lost the revision")
		return false
	}

	env.FS.UnkeepIdle(context.Background())
	if !marked() {
		t.Fatal("the mark of a file that still waits for its base was taken off")
	}
	if err := env.FS.Unlink(env.MustResolve("").Ino, "f.bin"); err != nil {
		t.Fatal(err)
	}
	uploadAll(t, env)
	env.FS.UnkeepIdle(context.Background())
	if marked() {
		t.Fatal("the revision of a file removed here stays marked Keep Forever")
	}
	if kept, err := env.FS.Store().KeptRevisions(); err != nil || len(kept) != 0 {
		t.Fatalf("the database still tells of marked revisions %v: %v", kept, err)
	}
}
