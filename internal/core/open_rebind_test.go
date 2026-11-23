package core_test

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

const rebindSize = 64 << 10

func changeInTheWindow(t *testing.T, env *testenv.Env, id string, times int32, seed uint64) *[]byte {
	t.Helper()
	var calls atomic.Int32
	last := new([]byte)
	env.FS.BeforeOpen(func() {
		n := calls.Add(1)
		if n > times {
			return
		}
		next := testenv.RandomBytes(rebindSize, seed+uint64(n))
		env.Emu.Update(id, func(f *emulator.File) { f.Content = next })
		poll(t, env)
		*last = next
	})
	return last
}

func writers(t *testing.T, env *testenv.Env, ino uint64) int {
	t.Helper()
	it, err := env.FS.Store().ByIno(meta.Ino(ino))
	if err != nil {
		t.Fatal(err)
	}
	st := env.FS.Edits().Lock(it)
	defer st.Unlock()
	return st.Writers()
}

func TestAnOpenOutdatedThreeTimesGetsEIOAndLeavesTheFile(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id := env.Emu.WriteFile(env.Emu.Root(), "f.bin", testenv.RandomBytes(rebindSize, 1971))
	env.Start()
	ino := env.MustResolve("f.bin").Ino
	last := changeInTheWindow(t, env, id, 3, 1972)
	_, err := env.FS.Open(context.Background(), ino, syscall.O_WRONLY|syscall.O_TRUNC)
	if !errors.Is(err, syscall.EIO) {
		t.Fatalf("an open outdated three times got %v, want EIO", err)
	}
	if n := writers(t, env, ino); n != 0 {
		t.Fatalf("the failed open left %d writers", n)
	}
	if it := item(t, env, "f.bin"); it.Size != rebindSize || it.Dirty {
		t.Fatalf("the failed open left the file %d bytes, changed here %v", it.Size, it.Dirty)
	}
	if got := readAll(t, env, "f.bin"); !bytes.Equal(got, *last) {
		t.Fatal("after the failed open the file reads other bytes than its last version")
	}
}

func TestAnOpenOutdatedTwiceGoesThrough(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id := env.Emu.WriteFile(env.Emu.Root(), "f.bin", testenv.RandomBytes(rebindSize, 1973))
	env.Start()
	last := changeInTheWindow(t, env, id, 2, 1974)
	fh, err := env.FS.Open(context.Background(), env.MustResolve("f.bin").Ino, syscall.O_RDONLY)
	if err != nil {
		t.Fatalf("an open outdated twice got %v", err)
	}
	defer env.FS.Release(fh)
	if got := mustRead(t, env, fh, 0, rebindSize); !bytes.Equal(got, *last) {
		t.Fatal("the open outdated twice reads other bytes than the last version")
	}
}

func TestAFollowOutdatedThreeTimesKeepsItsVersion(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	first := testenv.RandomBytes(rebindSize, 1975)
	id := env.Emu.WriteFile(env.Emu.Root(), "f.bin", first)
	env.Start()
	fh := openWriter(t, env, "f.bin", 0)
	defer env.FS.Release(fh)
	if got := mustRead(t, env, fh, 0, rebindSize); !bytes.Equal(got, first) {
		t.Fatal("scenario not built: the handle reads other bytes than the first version")
	}
	env.Emu.Update(id, func(f *emulator.File) { f.Content = testenv.RandomBytes(rebindSize, 1976) })
	poll(t, env)
	last := changeInTheWindow(t, env, id, 3, 1977)
	if got := mustRead(t, env, fh, 0, rebindSize); !bytes.Equal(got, first) {
		t.Fatal("a handle whose follow was outdated three times reads other bytes than its own version")
	}
	if got := mustRead(t, env, fh, 0, rebindSize); !bytes.Equal(got, *last) {
		t.Fatal("the next read with the window clean does not follow to the last version")
	}
}
