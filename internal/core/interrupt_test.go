package core_test

import (
	"bytes"
	"context"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func interrupted(t *testing.T, what string, call func(ctx context.Context) error) {
	t.Helper()
	ctx, interrupt := context.WithCancel(context.Background())
	defer interrupt()
	time.AfterFunc(100*time.Millisecond, interrupt)
	start := time.Now()
	err := call(ctx)
	testenv.RequireErrno(t, err, syscall.EINTR)
	if took := time.Since(start); took < 50*time.Millisecond || took >= slowMedia {
		t.Fatalf("the interrupted %s ended after %v, want when it was interrupted", what, took)
	}
}

const slowMedia = 3 * time.Second

func TestInterruptedReadAnswersEINTR(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	content := testenv.RandomBytes(200<<10, 710)
	env.Emu.WriteFile(env.Emu.Root(), "f.bin", content)
	env.Start()
	env.Emu.SetMediaDelay(slowMedia)
	fh := openFile(t, env, "f.bin")
	defer env.FS.Release(fh)
	interrupted(t, "read", func(ctx context.Context) error {
		_, err := env.FS.Read(ctx, fh, 0, make([]byte, 64<<10))
		return err
	})
	if n := cached(t, env, "f.bin"); n != 0 {
		t.Fatalf("after the interrupted read %d bytes count as fetched", n)
	}
	env.Emu.SetMediaDelay(0)
	if got := readAll(t, env, "f.bin"); !bytes.Equal(got, content) {
		t.Fatal("the file reads different after a read of it was interrupted")
	}
}

func TestInterruptedWriteAndCutAnswerEINTR(t *testing.T) {
	t.Parallel()
	for name, change := range map[string]func(ctx context.Context, env *testenv.Env, ino, fh uint64) error{
		"write": func(ctx context.Context, env *testenv.Env, _, fh uint64) error {
			_, err := env.FS.Write(ctx, fh, 1000, []byte("mine"))
			return err
		},
		"cut": func(ctx context.Context, env *testenv.Env, ino, _ uint64) error {
			return env.FS.Truncate(ctx, ino, 1004)
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			env := testenv.New(t)
			theirs := testenv.RandomBytes(200<<10, 711)
			id := env.Emu.WriteFile(env.Emu.Root(), "f.bin", theirs)
			env.Start()
			ino := env.MustResolve("f.bin").Ino
			fh := openWriter(t, env, "f.bin", 0)
			defer env.FS.Release(fh)
			env.Emu.SetMediaDelay(slowMedia)
			interrupted(t, name, func(ctx context.Context) error { return change(ctx, env, ino, fh) })
			if it := item(t, env, "f.bin"); it.Dirty || it.Size != int64(len(theirs)) {
				t.Fatalf("after the interrupted %s the file is dirty %v, %d bytes", name, it.Dirty, it.Size)
			}
			env.Emu.SetMediaDelay(0)
			if err := change(context.Background(), env, ino, fh); err != nil {
				t.Fatalf("the %s after the interrupted one: %v", name, err)
			}
			want := append([]byte(nil), theirs...)
			if name == "write" {
				copy(want[1000:], "mine")
			} else {
				want = want[:1004]
			}
			if got := mustRead(t, env, fh, 0, len(theirs)+1); !bytes.Equal(got, want) {
				t.Fatalf("after the %s the file reads %d bytes that differ from the %d expected", name, len(got), len(want))
			}
			env.FS.Release(fh)
			uploadAll(t, env)
			if f, _ := env.Emu.Get(id); !bytes.Equal(f.Content, want) {
				t.Fatalf("Drive holds %d bytes after the %s, want the %d expected", len(f.Content), name, len(want))
			}
		})
	}
}

func TestInterruptedAttributeAnswersEINTR(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Emu.WriteGoogleFile(env.Emu.Root(), "form", "application/vnd.google-apps.form")
	env.Start()
	ino := env.MustResolve("form.gform").Ino
	const attr = "user.drive.item_open_url"
	ctx, interrupt := context.WithCancel(context.Background())
	interrupt()
	_, err := env.FS.GetXattr(ctx, ino, attr)
	testenv.RequireErrno(t, err, syscall.EINTR)

	env.Emu.FailRequests(1000, 500, "backendError", 0)
	_, err = env.FS.GetXattr(context.Background(), ino, attr)
	testenv.RequireErrno(t, err, syscall.EIO)
	env.Emu.FailRequests(0, 0, "", 0)
	if got, err := env.FS.GetXattr(context.Background(), ino, attr); err != nil ||
		got != emulator.ViewLink("application/vnd.google-apps.form", item(t, env, "form.gform").ID) {
		t.Fatalf("the address reads %q, %v", got, err)
	}
}
