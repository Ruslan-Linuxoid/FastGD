package core_test

import (
	"errors"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestNestedEmptyFoldersUnderAFolderGoneFromDriveAreForgotten(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	top := env.Emu.Mkdir(env.Emu.Root(), "A")
	env.Start()
	mkdir(t, env, "A", "new1")
	mkdir(t, env, "A/new1", "new2")
	outer, inner := env.MustResolve("A/new1").Ino, env.MustResolve("A/new1/new2").Ino
	env.Emu.Delete(top)
	poll(t, env)
	for name, ino := range map[string]uint64{"A/new1": outer, "A/new1/new2": inner} {
		if _, err := env.FS.Store().ByIno(meta.Ino(ino)); !errors.Is(err, meta.ErrNotFound) {
			t.Fatalf("the empty folder %s made here stays after its folder went from Drive: %v", name, err)
		}
	}
}
