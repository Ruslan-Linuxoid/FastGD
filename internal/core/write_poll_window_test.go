package core_test

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/cache"
	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

const pollWindowPage = 4096

func outdatedRow(t *testing.T, env *testenv.Env) (meta.Item, []byte) {
	t.Helper()
	id := env.Emu.WriteFile(env.Emu.Root(), "f.bin", testenv.RandomBytes(4*pollWindowPage, 1921))
	env.Start()
	it, err := env.FS.Store().ByIno(meta.Ino(env.MustResolve("f.bin").Ino))
	if err != nil {
		t.Fatal(err)
	}
	next := testenv.RandomBytes(4*pollWindowPage, 1922)
	env.Emu.Update(id, func(f *emulator.File) { f.Content = next })
	poll(t, env)
	return it, next
}

func TestAFirstWriteOnARowAPollOutdatedBuildsOnTheNewVersion(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	it, next := outdatedRow(t, env)
	data := testenv.RandomBytes(pollWindowPage, 1923)
	if err := env.FS.Edits().Write(context.Background(), it, pollWindowPage, data); err != nil {
		t.Fatal(err)
	}
	want := bytes.Clone(next)
	copy(want[pollWindowPage:], data)
	if !bytes.Equal(readAll(t, env, "f.bin"), want) {
		t.Fatal("the write built on other bytes than the version the poll brought")
	}
}

func TestACutOnARowAPollOutdatedKeepsTheNewVersion(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	it, next := outdatedRow(t, env)
	if err := env.FS.Edits().Truncate(context.Background(), it, 2*pollWindowPage); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(readAll(t, env, "f.bin"), next[:2*pollWindowPage]) {
		t.Fatal("the cut kept other bytes than those of the version the poll brought")
	}
}

func TestAFetchIntoContentAPollReplacedIsStale(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id := env.Emu.WriteFile(env.Emu.Root(), "f.bin", testenv.RandomBytes(4*pollWindowPage, 1924))
	env.Start()
	ino := meta.Ino(env.MustResolve("f.bin").Ino)
	it, err := env.FS.Store().ByIno(ino)
	if err != nil {
		t.Fatal(err)
	}
	gen, err := env.FS.Generation(ino)
	if err != nil {
		t.Fatal(err)
	}
	env.Emu.Update(id, func(f *emulator.File) { f.Content = testenv.RandomBytes(4*pollWindowPage, 1925) })
	poll(t, env)
	fetchErr, checkErr := env.FS.FetchIn(context.Background(), it, gen)
	if !errors.Is(fetchErr, cache.ErrStale) || !errors.Is(checkErr, cache.ErrStale) {
		t.Fatalf("fetch %v, check %v; want both stale", fetchErr, checkErr)
	}
	if used := env.FS.CacheUsed(); used != 0 {
		t.Fatalf("the cache counts %d bytes after a stale fetch", used)
	}
}

func TestAPollBeforeTheFetchOfAFirstWriteLeavesItOnTheNewVersion(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id := env.Emu.WriteFile(env.Emu.Root(), "f.bin", testenv.RandomBytes(4*pollWindowPage, 1926))
	env.Start()
	next := testenv.RandomBytes(4*pollWindowPage, 1927)
	var once sync.Once
	env.FS.BeforeFetch(func() {
		once.Do(func() {
			env.Emu.Update(id, func(f *emulator.File) { f.Content = next })
			poll(t, env)
		})
	})
	it, err := env.FS.Store().ByIno(meta.Ino(env.MustResolve("f.bin").Ino))
	if err != nil {
		t.Fatal(err)
	}
	data := testenv.RandomBytes(pollWindowPage, 1928)
	if err := env.FS.Edits().Write(context.Background(), it, pollWindowPage, data); err != nil {
		t.Fatal(err)
	}
	want := bytes.Clone(next)
	copy(want[pollWindowPage:], data)
	if !bytes.Equal(readAll(t, env, "f.bin"), want) {
		t.Fatal("the write built on other bytes than the version the poll brought")
	}
}
