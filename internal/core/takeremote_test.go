package core_test

import (
	"slices"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestGivingWayThatFailsAfterTheRowDropsTheKernelsPages(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	id := env.Emu.WriteFile(env.Emu.Root(), "f.bin", testenv.RandomBytes(64<<10, 1550))
	killed := killAfter(env, core.TakeRemoteStep)
	env.Start()
	ino := env.MustResolve("f.bin").Ino
	fh := openWriter(t, env, "f.bin", 0)
	write(t, env, fh, 100, []byte("edit"))
	env.FS.Release(fh)
	if err := env.FS.Unlink(env.MustResolve("").Ino, "f.bin"); err != nil {
		t.Fatal(err)
	}
	env.Emu.Update(id, func(f *emulator.File) { f.Content = testenv.RandomBytes(32<<10, 1551) })
	rec := &recorder{}
	env.FS.SetNotifier(rec)
	rounds(env, 3)
	if !killed.Load() {
		t.Fatal("scenario not built: the giving way did not reach the step")
	}
	waitUntil(t, func() bool {
		rec.mu.Lock()
		defer rec.mu.Unlock()
		return slices.Contains(rec.inodes, fmtInode(ino, true))
	})
}
