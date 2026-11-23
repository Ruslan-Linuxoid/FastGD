package core_test

import (
	"bytes"
	"context"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestClockSetBackHoldsNoUpload(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	ctx := context.Background()

	writeFile(t, env, "", "again.txt", []byte("first"))
	uploadAll(t, env)
	fh := openWriter(t, env, "again.txt", 0)
	write(t, env, fh, 0, []byte("second"))
	env.FS.Release(fh)

	writeFile(t, env, "", "failed.txt", []byte("failed once"))
	env.Emu.SetOffline(true)
	env.FS.SyncOnce(ctx)
	env.Emu.SetOffline(false)
	if it := item(t, env, "failed.txt"); it.Attempts != 1 || !it.NextTry.After(env.Clock.Now()) {
		t.Fatalf("scenario not built: %d tries, the next at %v", it.Attempts, it.NextTry)
	}

	_, empty, err := env.FS.Create(env.MustResolve("").Ino, "empty.txt", syscall.O_RDWR)
	if err != nil {
		t.Fatal(err)
	}
	defer env.FS.Release(empty)

	env.Clock.Advance(-24 * time.Hour)

	for range 20 {
		env.Clock.Advance(time.Minute)
		env.FS.SyncOnce(ctx)
	}
	for name, want := range map[string]string{"again.txt": "second", "failed.txt": "failed once", "empty.txt": ""} {
		f, ok := env.Emu.ByPath(name)
		if !ok || !bytes.Equal(f.Content, []byte(want)) {
			t.Fatalf("twenty minutes after the clock went back a day, Drive holds %q as %q, found %v; want %q", name, f.Content,
				ok, want)
		}
	}
}
