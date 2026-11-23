package core_test

import (
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestRenameHereAndInDriveKeepsDrivesName(t *testing.T) {
	t.Parallel()
	for way, viaPoll := range map[string]bool{"from changes": true, "before the upload": false} {
		t.Run(way, func(t *testing.T) {
			t.Parallel()
			env := testenv.New(t)
			dir := env.Emu.Mkdir(env.Emu.Root(), "d")
			id := env.Emu.WriteFile(dir, "a.txt", []byte("x"))
			env.Start()
			rename(t, env, "d", "a.txt", "d", "mine.txt")
			env.Emu.Update(id, func(f *emulator.File) { f.Name = "theirs.txt" })
			if viaPoll {
				poll(t, env)
			}
			uploadAll(t, env)
			if names := env.Names("d"); len(names) != 1 || names[0] != "theirs.txt" {
				t.Fatalf("the folder holds %q, want Drive's name", names)
			}
			if f, _ := env.Emu.Get(id); f.Name != "theirs.txt" {
				t.Fatalf("Drive holds the name %q", f.Name)
			}
			if len(env.Logs("mine.txt")) == 0 {
				t.Fatal("the log does not tell of the rename that was undone")
			}
		})
	}
}

func TestRenameAndTouchHereKeepsDrivesNameInOneUpdate(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	dir := env.Emu.Mkdir(env.Emu.Root(), "d")
	id := env.Emu.WriteFile(dir, "a.txt", []byte("x"))
	env.Start()
	rename(t, env, "d", "a.txt", "d", "mine.txt")
	touched := env.Clock.Now().Add(time.Hour).Truncate(time.Millisecond)
	if err := env.FS.SetMtime(env.MustResolve("d/mine.txt").Ino, touched); err != nil {
		t.Fatal(err)
	}
	env.Emu.Update(id, func(f *emulator.File) { f.Name = "theirs.txt" })
	env.Emu.ResetRequests()
	uploadAll(t, env)
	f, _ := env.Emu.Get(id)
	if f.Name != "theirs.txt" || !f.Modified.Equal(touched) {
		t.Fatalf("Drive holds %q of %v, want Drive's name with the time set here", f.Name, f.Modified)
	}
	if _, updates := uploads(env); updates != 1 {
		t.Fatalf("the round sent %d updates, want one", updates)
	}
	if it := item(t, env, "d/theirs.txt"); it.MetaDirty {
		t.Fatal("the file still has metadata to send")
	}
}
