package core_test

import (
	"context"
	"syscall"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/cache"
	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func changedUnfetched(t *testing.T, env *testenv.Env) {
	t.Helper()
	it := item(t, env, "f.bin")
	if err := env.FS.MarkChangedHere(uint64(it.Ino)); err != nil {
		t.Fatal(err)
	}
	if it = item(t, env, "f.bin"); !it.Dirty || it.Base != it.Size || cached(t, env, "f.bin") != 0 {
		t.Fatalf("scenario not built: the file is changed here %v with %d of its %d bytes from Drive", it.Dirty, it.Base,
			it.Size)
	}
}

func fromDrive(t *testing.T, size int, seed uint64) (*testenv.Env, []byte) {
	t.Helper()
	env := testenv.New(t)
	return env, startWith(env, size, seed)
}

func startWith(env *testenv.Env, size int, seed uint64) []byte {
	content := testenv.RandomBytes(size, seed)
	env.Emu.WriteFile(env.Emu.Root(), "f.bin", content)
	env.Start()
	env.Emu.ResetRequests()
	return content
}

func readClosed(t *testing.T, env *testenv.Env, path string) []byte {
	t.Helper()
	a := env.MustResolve(path)
	fh, err := env.FS.Open(context.Background(), a.Ino, syscall.O_RDONLY)
	if err != nil {
		t.Fatal(err)
	}
	defer env.FS.Release(fh)
	got := mustRead(t, env, fh, 0, int(a.Size))
	waitUntil(t, func() bool { return cached(t, env, path) == a.Size })
	return got
}

func giveID(t *testing.T, env *testenv.Env, path string) string {
	t.Helper()
	id, err := env.FS.GiveID(env.MustResolve(path).Ino)
	if err != nil || meta.IsLocalKey(id) {
		t.Fatalf("give %s a fileId: %q, %v", path, id, err)
	}
	return id
}

func contentFile(env *testenv.Env, id string) string {
	return cache.PathIn(core.ContentDir(env.Dir), id)
}
