package core_test

import (
	"bytes"
	"context"
	"sync"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestAWriterThatReadInTheWindowOfAPollKeepsTheSizeOfItsVersion(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	window := inPollWindow(env)
	id := env.Emu.WriteFile(env.Emu.Root(), "f.bin", testenv.RandomBytes(64<<10, 1931))
	env.Start()
	fh := openWriter(t, env, "f.bin", 0)
	defer env.FS.Release(fh)
	next := testenv.RandomBytes(128<<10, 1932)
	env.Emu.Update(id, func(f *emulator.File) { f.Content = next })
	got := make([]byte, len(next))
	var err error
	wait := window(func() { _, err = env.FS.Read(context.Background(), fh, 0, got) })
	poll(t, env)
	wait()
	if err != nil || !bytes.Equal(got, next) {
		t.Fatalf("scenario not built: the read in the window got %v", err)
	}
	env.Emu.Delete(id)
	poll(t, env)
	n, err := env.FS.Read(context.Background(), fh, 0, got)
	if err != nil || n != len(next) || !bytes.Equal(got, next) {
		t.Fatalf("once the file is gone from Drive the handle read %d bytes, %v; want the %d of its version",
			n, err, len(next))
	}
}

func TestAnOpenAcrossAPollKeepsTheSizeOfItsVersion(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id := env.Emu.WriteFile(env.Emu.Root(), "f.bin", testenv.RandomBytes(64<<10, 1933))
	env.Start()
	next := testenv.RandomBytes(128<<10, 1934)
	var once sync.Once
	env.FS.BeforeOpen(func() {
		once.Do(func() {
			env.Emu.Update(id, func(f *emulator.File) { f.Content = next })
			poll(t, env)
		})
	})
	fh := openFile(t, env, "f.bin")
	defer env.FS.Release(fh)
	if got := mustRead(t, env, fh, 0, len(next)); !bytes.Equal(got, next) {
		t.Fatal("scenario not built: the file opened across the poll reads other bytes than the new version")
	}
	env.Emu.Delete(id)
	poll(t, env)
	got := make([]byte, len(next))
	n, err := env.FS.Read(context.Background(), fh, 0, got)
	if err != nil || n != len(next) || !bytes.Equal(got, next) {
		t.Fatalf("once the file is gone from Drive the handle read %d bytes, %v; want the %d of its version",
			n, err, len(next))
	}
}

func TestAWriterFollowingAcrossAPollKeepsTheSizeOfItsVersion(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id := env.Emu.WriteFile(env.Emu.Root(), "f.bin", testenv.RandomBytes(64<<10, 1935))
	env.Start()
	fh := openWriter(t, env, "f.bin", 0)
	defer env.FS.Release(fh)
	env.Emu.Update(id, func(f *emulator.File) { f.Content = testenv.RandomBytes(96<<10, 1936) })
	poll(t, env)
	last := testenv.RandomBytes(128<<10, 1937)
	var once sync.Once
	env.FS.BeforeOpen(func() {
		once.Do(func() {
			env.Emu.Update(id, func(f *emulator.File) { f.Content = last })
			poll(t, env)
		})
	})
	got := make([]byte, len(last))

	for range 2 {
		if _, err := env.FS.Read(context.Background(), fh, 0, got); err != nil {
			t.Fatal(err)
		}
	}
	if !bytes.Equal(got, last) {
		t.Fatal("scenario not built: the handle does not read the last version")
	}
	env.Emu.Delete(id)
	poll(t, env)
	n, err := env.FS.Read(context.Background(), fh, 0, got)
	if err != nil || n != len(last) || !bytes.Equal(got, last) {
		t.Fatalf("once the file is gone from Drive the handle read %d bytes, %v; want the %d of its version",
			n, err, len(last))
	}
}
