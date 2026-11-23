package core_test

import (
	"bytes"
	"context"
	"errors"
	"sync/atomic"
	"syscall"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestAFirstWriteOfTheKeeperInTheWindowOfAPollBuildsOnTheNewVersion(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	window := inPollWindow(env)
	id := env.Emu.WriteFile(env.Emu.Root(), "f.bin", testenv.RandomBytes(4<<12, 1961))
	env.Start()
	it, err := env.FS.Store().ByIno(meta.Ino(env.MustResolve("f.bin").Ino))
	if err != nil {
		t.Fatal(err)
	}
	next := testenv.RandomBytes(4<<12, 1962)
	env.Emu.Update(id, func(f *emulator.File) { f.Content = next })
	data := testenv.RandomBytes(1<<12, 1963)
	wait := window(func() { err = env.FS.Edits().Write(context.Background(), it, 1<<12, data) })
	poll(t, env)
	wait()
	if err != nil {
		t.Fatal(err)
	}
	want := bytes.Clone(next)
	copy(want[1<<12:], data)
	if !bytes.Equal(readAll(t, env, "f.bin"), want) {
		t.Fatal("the write in the window of the poll built on other bytes than the new version")
	}
}

func TestAWriteInTheWindowOfAPollWeighsTheRoomOfTheNewVersion(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	window := inPollWindow(env)
	var free atomic.Int64
	free.Store(501 << 20)
	tune := env.Tune
	env.Tune = func(o *core.Options) {
		tune(o)
		o.CacheFree = func() (int64, error) { return free.Load(), nil }
	}
	id := env.Emu.WriteFile(env.Emu.Root(), "f.bin", testenv.RandomBytes(64<<10, 1964))
	env.Start()
	fh := openWriter(t, env, "f.bin", 0)
	defer env.FS.Release(fh)
	env.Emu.Update(id, func(f *emulator.File) { f.Content = testenv.RandomBytes(8<<20, 1965) })
	var err error
	wait := window(func() { _, err = env.FS.Write(context.Background(), fh, 0, []byte("edit")) })
	poll(t, env)
	wait()
	if !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("the write in the window of the poll got %v, want ENOSPC for the room of the new version", err)
	}
}

func TestACutInTheWindowOfAPollTakesTheRightsOfTheNewVersion(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	window := inPollWindow(env)
	id := env.Emu.WriteFile(env.Emu.Root(), "f.bin", testenv.RandomBytes(64<<10, 1966))
	env.Start()
	ino := env.MustResolve("f.bin").Ino
	env.Emu.Update(id, func(f *emulator.File) {
		f.Content = testenv.RandomBytes(64<<10, 1967)
		f.CannotDownload = true
	})
	var err error
	wait := window(func() { err = env.FS.Truncate(context.Background(), ino, 100) })
	poll(t, env)
	wait()
	if !errors.Is(err, syscall.EACCES) {
		t.Fatalf("the cut in the window of the poll got %v, want EACCES for the rights of the new version", err)
	}
}
