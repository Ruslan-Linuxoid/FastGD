package core_test

import (
	"context"
	"strings"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

const gib = int64(1) << 30

func roomyCache(t *testing.T) *testenv.Env {
	t.Helper()
	env := testenv.New(t)
	env.CacheSize = 100 * gib
	env.Tune = func(o *core.Options) {
		o.CacheFree = func() (int64, error) { return 10_000 * gib, nil }
	}
	env.Start()
	return env
}

func refreshQuota(t *testing.T, env *testenv.Env) {
	t.Helper()
	if err := env.FS.RefreshQuota(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestTheFilesOfDriveBoundTheCacheLimit(t *testing.T) {
	t.Parallel()
	env := roomyCache(t)
	if got := env.FS.CacheLimit(); got != 100*gib {
		t.Fatalf("before Drive tells what it holds the limit is %d, want cache_size", got)
	}

	env.Emu.SetFilesUsage(60*gib, 10*gib)
	env.Emu.SetQuota(0, 30*gib)
	refreshQuota(t, env)
	if got := env.FS.CacheLimit(); got != 5*gib {
		t.Fatalf("with 50 GiB of files in Drive the limit is %d, want 5 GiB", got)
	}
	logged := env.Logs("the cache limit changed")
	if len(logged) == 0 || !strings.Contains(logged[len(logged)-1], "by=drive") {
		t.Fatalf("the log does not tell that Drive set the limit: %q", logged)
	}
}

func TestASmallDriveLeavesTheCacheOneGiB(t *testing.T) {
	t.Parallel()
	env := roomyCache(t)
	env.Emu.SetFilesUsage(5*gib, 0)
	refreshQuota(t, env)
	if got := env.FS.CacheLimit(); got != gib {
		t.Fatalf("with 5 GiB of files in Drive the limit is %d, want 1 GiB", got)
	}
}

func TestTheLimitFollowsTheFilesOfDrive(t *testing.T) {
	t.Parallel()
	env := roomyCache(t)
	env.Emu.SetFilesUsage(50*gib, 0)
	refreshQuota(t, env)
	env.Emu.SetFilesUsage(200*gib, 0)
	if got := env.FS.CacheLimit(); got != 5*gib {
		t.Fatalf("before the next read of the quota the limit is %d, want 5 GiB", got)
	}
	refreshQuota(t, env)
	if got := env.FS.CacheLimit(); got != 20*gib {
		t.Fatalf("with 200 GiB of files in Drive the limit is %d, want 20 GiB", got)
	}
	env.Emu.SetFilesUsage(30*gib, 0)
	refreshQuota(t, env)
	if got := env.FS.CacheLimit(); got != 3*gib {
		t.Fatalf("once files went from Drive the limit is %d, want 3 GiB", got)
	}
	env.Emu.SetOffline(true)
	env.Restart()
	if got := env.FS.CacheLimit(); got != 3*gib {
		t.Fatalf("after a restart without the network the limit is %d, want 3 GiB", got)
	}
}

func TestWhatLiesOutsideDriveLeavesTheLimit(t *testing.T) {
	t.Parallel()
	env := roomyCache(t)
	env.Emu.SetFilesUsage(50*gib, 0)
	refreshQuota(t, env)
	env.Emu.SetQuota(0, 500*gib)
	refreshQuota(t, env)
	if got := env.FS.CacheLimit(); got != 5*gib {
		t.Fatalf("with 500 GiB outside Drive the limit is %d, want 5 GiB", got)
	}
}

func TestWithoutAnAnswerOfDriveTheDiskBoundsTheLimit(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.CacheSize = 100 * gib
	env.Tune = func(o *core.Options) { o.CacheFree = func() (int64, error) { return 20 * gib, nil } }
	env.Emu.SetFilesUsage(10*gib, 0)
	env.Start()
	if got, want := env.FS.CacheLimit(), 4*gib; got < want || got > want+gib/10 {
		t.Fatalf("with 20 GiB free on the disk the limit is %d, want a fifth of the disk", got)
	}
}

func TestStatusTellsTheLimitAndWhatSetIt(t *testing.T) {
	t.Parallel()
	env := roomyCache(t)
	if st := readStatus(t, env); st.CacheLimit != 100*gib || st.CacheLimitBy != "cache_size" {
		t.Fatalf("before Drive tells what it holds status reads the limit %d by %q", st.CacheLimit, st.CacheLimitBy)
	}
	env.Emu.SetFilesUsage(5*gib, 0)
	refreshQuota(t, env)
	if st := readStatus(t, env); st.CacheLimit != gib || st.CacheLimitBy != "drive" {
		t.Fatalf("with 5 GiB of files in Drive status reads the limit %d by %q, want 1 GiB by drive", st.CacheLimit,
			st.CacheLimitBy)
	}
}
