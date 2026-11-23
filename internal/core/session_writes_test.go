package core_test

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestWriteAfterTheSessionStartedUploadsAgain(t *testing.T) {
	t.Parallel()
	for _, restartFirst := range []bool{false, true} {
		t.Run(fmt.Sprintf("restart first %v", restartFirst), func(t *testing.T) {
			t.Parallel()
			env := testenv.New(t)
			smallSessions(env)
			env.Start()
			content := testenv.RandomBytes(sessionFrom+8<<20, 55)
			upToFirstChunk(t, env, content)
			if restartFirst {
				env.Restart()
			}

			fh, err := env.FS.Open(context.Background(), env.MustResolve("f.bin").Ino, syscall.O_WRONLY)
			if err != nil {
				t.Fatal(err)
			}
			write(t, env, fh, 0, []byte("changed"))
			env.FS.Release(fh)
			copy(content, "changed")
			ses, err := env.FS.Store().UploadSession(meta.Ino(env.MustResolve("f.bin").Ino))
			if err != nil || ses.Address != "" {
				t.Fatalf("after a write the session is %q (%v)", ses.Address, err)
			}

			env.Restart()
			env.Emu.ResetRequests()
			uploadAll(t, env)
			if u := uploadsSeen(t, env); u.sessions != 1 || !slices.Contains(u.chunks, 0) {
				t.Fatalf("after a write the upload went as %+v, want a new session from the start", u)
			}
			if f := inDrive(t, env, "f.bin"); md5Of(f.Content) != md5Of(content) {
				t.Fatal("the file went up different")
			}
		})
	}
}

func TestRenameDuringASessionReachesDrive(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	smallSessions(env)
	env.Start()
	content := testenv.RandomBytes(sessionFrom+8<<20, 59)
	upToFirstChunk(t, env, content)
	root := env.MustResolve("").Ino
	if err := env.FS.Rename(root, "f.bin", root, "g.bin", 0); err != nil {
		t.Fatal(err)
	}
	env.Restart()
	uploadAll(t, env)
	if f := inDrive(t, env, "g.bin"); md5Of(f.Content) != md5Of(content) {
		t.Fatal("the renamed file went up different")
	}
	if n := len(env.Emu.Children(env.Emu.Root())); n != 1 {
		t.Fatalf("Drive holds %d items after the rename, want 1", n)
	}
}

func TestWriteDuringTheLastChunkNeverMixesVersions(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	smallSessions(env)
	env.Start()
	const size = sessionFrom + 100<<10
	content := testenv.RandomBytes(size, 90)
	writeFile(t, env, "", "f.bin", content)
	patch := testenv.RandomBytes(64<<10, 91)
	wrote := false
	env.FS.BeforeChunk(func(from int64) {

		if wrote || from < size-100<<10 {
			return
		}
		wrote = true
		fh := openWriter(t, env, "f.bin", 0)
		write(t, env, fh, size-64<<10, patch)
		write(t, env, fh, 0, patch)
		env.FS.Release(fh)
	})
	env.Clock.Advance(20 * time.Minute)
	env.FS.SyncOnce(context.Background())
	if !wrote {
		t.Fatal("no chunk carried the end of the file")
	}

	if f := inDrive(t, env, "f.bin"); md5Of(f.Content) != md5Of(content) {
		t.Fatal("Drive holds a version that never was here")
	}
	if it := item(t, env, "f.bin"); !it.Dirty {
		t.Fatal("the file written during its upload counts as clean")
	}
	want := append([]byte(nil), content...)
	copy(want[size-64<<10:], patch)
	copy(want, patch)
	uploadAll(t, env)
	if f := inDrive(t, env, "f.bin"); md5Of(f.Content) != md5Of(want) {
		t.Fatal("the written file went up different")
	}
}

func TestWriteBeforeTheLastChunkCompletesNothing(t *testing.T) {
	t.Parallel()
	for name, when := range map[string]func(from int64) bool{
		"between chunks":       func(from int64) bool { return from > 0 },
		"while the tail seals": func(from int64) bool { return from == core.Sealing },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			env := testenv.New(t)
			smallSessions(env)
			env.Start()

			const size = sessionFrom + 4<<20 + 100<<10
			content := testenv.RandomBytes(size, 92)
			writeFile(t, env, "", "f.bin", content)
			wrote := false
			env.FS.BeforeChunk(func(from int64) {
				if wrote || !when(from) {
					return
				}
				wrote = true
				fh := openWriter(t, env, "f.bin", 0)
				write(t, env, fh, 0, []byte("changed"))
				env.FS.Release(fh)
			})
			env.Clock.Advance(20 * time.Minute)
			env.FS.SyncOnce(context.Background())
			if !wrote {
				t.Fatal("the write never came")
			}
			for _, f := range env.Emu.Children(env.Emu.Root()) {
				if f.Name == "f.bin" {
					t.Fatal("a session written into before its end put a file in Drive")
				}
			}
			copy(content, "changed")
			uploadAll(t, env)
			if f := inDrive(t, env, "f.bin"); md5Of(f.Content) != md5Of(content) {
				t.Fatal("the written file went up different")
			}
		})
	}
}

func TestSessionOfAFileRemovedHereGoesWithIt(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id := env.Emu.WriteFile(env.Emu.Root(), "f.bin", testenv.RandomBytes(sessionFrom, 1380))
	smallSessions(env)
	env.Start()
	rewrite := func(seed uint64) []byte {
		content := testenv.RandomBytes(sessionFrom, seed)
		fh := openWriter(t, env, "f.bin", syscall.O_TRUNC)
		write(t, env, fh, 0, content)
		env.FS.Release(fh)
		return content
	}
	rewrite(1381)

	env.Emu.SetUploadLimitReached(true)
	env.Emu.RefuseUploadsOnChunks(true)
	env.Clock.Advance(20 * time.Minute)
	env.FS.SyncOnce(context.Background())
	ino := meta.Ino(env.MustResolve("f.bin").Ino)
	if ses, err := env.FS.Store().UploadSession(ino); err != nil || ses.Address == "" {
		t.Fatalf("scenario not built: the cut upload left no session (%v)", err)
	}
	if err := env.FS.Unlink(env.MustResolve("").Ino, "f.bin"); err != nil {
		t.Fatal(err)
	}
	env.Emu.SetUploadLimitReached(false)
	env.Emu.RefuseUploadsOnChunks(false)
	uploadAll(t, env)
	if ses, err := env.FS.Store().UploadSession(ino); err != nil || ses.Address != "" {
		t.Fatalf("the session of a file removed here stays (%v)", err)
	}
	env.Emu.Update(id, func(f *emulator.File) { f.Trashed = false })
	poll(t, env)
	want := rewrite(1382)
	env.Emu.ResetRequests()
	uploadAll(t, env)
	if u := uploadsSeen(t, env); u.sessions != 1 {
		t.Fatalf("the new content went up in %d new sessions, want one", u.sessions)
	}
	if !bytes.Equal(inDrive(t, env, "f.bin").Content, want) {
		t.Fatal("Drive holds other content")
	}
}
