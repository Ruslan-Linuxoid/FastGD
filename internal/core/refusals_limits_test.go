package core_test

import (
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestRefusalsByTheLimitsTakeALineInTenMinutes(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Tune = func(o *core.Options) { o.MaxChildren, o.MaxDepth = 1, 2 }
	env.Start()
	mkdir(t, env, "", "full")
	writeFile(t, env, "full", "a", []byte("a"))
	mkdir(t, env, "", "x")
	mkdir(t, env, "x", "y")
	full, deep := env.MustResolve("full").Ino, env.MustResolve("x/y").Ino
	refuse := func() {
		t.Helper()
		for _, in := range []uint64{full, deep} {
			if _, err := env.FS.Mkdir(in, "d"); err != syscall.ENOSPC {
				t.Fatalf("a mkdir past a limit got %v, want ENOSPC", err)
			}
		}
	}
	for range 1000 {
		refuse()
		env.Clock.Advance(60 * time.Millisecond)
	}
	env.Clock.Advance(10 * time.Minute)
	refuse()
	for _, msg := range []string{"a Drive folder holds no more items", "Drive keeps folders no deeper"} {
		if got := repeatsTold(env.Logs(msg)); !slices.Equal(got, []string{"", "999"}) {
			t.Errorf("the lines %q tell the repeats %q, want none, then 999", msg, got)
		}
	}
}
