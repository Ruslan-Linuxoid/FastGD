package core_test

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func readStatus(t *testing.T, env *testenv.Env) meta.Status {
	t.Helper()
	ro, err := meta.OpenReadOnly(filepath.Join(env.Dir, "meta.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	st, err := ro.ReadStatus(env.Clock.Now().Add(-24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func requireRangesAskedOnce(t *testing.T, env *testenv.Env) {
	t.Helper()

	if len(mediaRanges(env)) == 0 {
		t.Fatal("the read asked Drive nothing")
	}
	seen := map[string]bool{}
	for _, r := range mediaRanges(env) {
		if seen[r] {
			t.Fatalf("the range %s was asked twice: %v", r, mediaRanges(env))
		}
		seen[r] = true
	}
}

func TestDownloadQuotaOfAFileFailsAtOnceAndShows(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	content := testenv.RandomBytes(64*kib, 61)
	id := env.Emu.WriteFile(env.Emu.Root(), "f.bin", content)
	env.Emu.WriteFile(env.Emu.Root(), "other.bin", []byte("other"))
	env.Start()
	fh := openFile(t, env, "f.bin")
	mustRead(t, env, fh, 0, 8*kib)
	waitUntil(t, func() bool { return cachedBytes(pieces(t, env, "f.bin")) == 8*kib })
	if st := readStatus(t, env); !st.DownloadRefused.IsZero() {
		t.Fatalf("a refusal shows before any: %+v", st)
	}

	env.Emu.SetFileDownloadQuotaExceeded(id, true)
	env.Emu.ResetRequests()
	_, err := env.FS.Read(context.Background(), fh, 32*kib, make([]byte, 4*kib))
	testenv.RequireErrno(t, err, syscall.EIO)
	requireRangesAskedOnce(t, env)
	if got := mustRead(t, env, fh, 0, 8*kib); !bytes.Equal(got, content[:8*kib]) {
		t.Fatal("the downloaded pieces read different after the refusal")
	}
	if got := readAll(t, env, "other.bin"); string(got) != "other" {
		t.Fatalf("another file reads %q", got)
	}
	st := readStatus(t, env)
	if !st.DownloadRefused.Equal(env.Clock.Now()) || st.RefusedPath != "My Drive/f.bin" {
		t.Fatalf("status shows the refusal as %v on %q", st.DownloadRefused, st.RefusedPath)
	}
	if len(env.Logs("download quota")) == 0 {
		t.Fatal("the log does not tell of the refusal")
	}

	env.Restart()
	if st := readStatus(t, env); st.RefusedPath != "My Drive/f.bin" {
		t.Fatalf("after a restart status shows the refusal on %q", st.RefusedPath)
	}
	env.Emu.SetFileDownloadQuotaExceeded(id, false)
	fh = openFile(t, env, "f.bin")
	if got := mustRead(t, env, fh, 32*kib, 4*kib); !bytes.Equal(got, content[32*kib:36*kib]) {
		t.Fatal("the file reads different once Drive lets it download")
	}

	waitUntil(t, func() bool { return readStatus(t, env).DownloadRefused.IsZero() })
}

func TestDownloadQuotaOfTheAccountLeavesCachedFilesReadable(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	cached := testenv.RandomBytes(4*kib, 62)
	env.Emu.WriteFile(env.Emu.Root(), "cached.bin", cached)
	env.Emu.WriteFile(env.Emu.Root(), "cold.bin", []byte("cold"))
	env.Start()
	readAll(t, env, "cached.bin")
	env.Emu.SetDownloadQuotaExceeded(true)
	fh := openFile(t, env, "cold.bin")
	_, err := env.FS.Read(context.Background(), fh, 0, make([]byte, 4))
	testenv.RequireErrno(t, err, syscall.EIO)
	if got := readAll(t, env, "cached.bin"); !bytes.Equal(got, cached) {
		t.Fatal("a cached file reads different under the account's download quota")
	}
	if st := readStatus(t, env); st.RefusedPath != "My Drive/cold.bin" {
		t.Fatalf("status shows the refusal on %q", st.RefusedPath)
	}
}

func TestUsageCountsOutliveARestartAndLeaveAfterADay(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Emu.WriteFile(env.Emu.Root(), "a.bin", testenv.RandomBytes(8*kib, 63))
	env.Emu.WriteFile(env.Emu.Root(), "b.bin", testenv.RandomBytes(4*kib, 64))
	env.Start()
	env.FS.SaveUsage()
	before := readStatus(t, env)
	if before.DayUnits == 0 {
		t.Fatal("the first load of My Drive spent no units")
	}

	readAll(t, env, "a.bin")
	env.FS.SaveUsage()
	after := readStatus(t, env)
	if after.DayBytes-before.DayBytes != 8*kib || after.DayUnits-before.DayUnits != 200 {
		t.Fatalf("a download of 8 KiB counted %d bytes and %d units", after.DayBytes-before.DayBytes,
			after.DayUnits-before.DayUnits)
	}

	readAll(t, env, "b.bin")
	env.Restart()
	if st := readStatus(t, env); st.DayBytes != after.DayBytes+4*kib || st.DayUnits != after.DayUnits+200 {
		t.Fatalf("after a restart the counts are %d bytes and %d units, want %d and %d",
			st.DayBytes, st.DayUnits, after.DayBytes+4*kib, after.DayUnits+200)
	}
	env.Clock.Advance(25 * time.Hour)
	env.FS.SaveUsage()
	if st := readStatus(t, env); st.DayBytes != 0 || st.DayUnits != 0 {
		t.Fatalf("a day later the counts are %d bytes and %d units", st.DayBytes, st.DayUnits)
	}
}

func usageWarnings(env *testenv.Env, kind string) int {
	return len(env.Logs(kind + " the project's free threshold"))
}

func requireWarnings(t *testing.T, env *testenv.Env, wantNears, wantPassed int, when string) {
	t.Helper()
	nears, passed := usageWarnings(env, "nears"), usageWarnings(env, "passed")
	if nears != wantNears || passed != wantPassed {
		t.Fatalf("%s the log warns %d times that the count nears and %d that it passed, want %d and %d",
			when, nears, passed, wantNears, wantPassed)
	}
}

func TestUsageWarnsNearAndPastTheFreeThresholdOnce(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Tune = func(o *core.Options) { o.FreeBytes = 100 * kib }
	files := map[string][]byte{}
	for i, name := range []string{"a.bin", "b.bin", "c.bin", "d.bin", "e.bin", "f.bin", "g.bin"} {
		files[name] = testenv.RandomBytes(30*kib, uint64(65+i))
		env.Emu.WriteFile(env.Emu.Root(), name, files[name])
	}
	env.Start()
	for _, name := range []string{"a.bin", "b.bin"} {
		readAll(t, env, name)
		env.FS.SaveUsage()
	}
	requireWarnings(t, env, 0, 0, "at 60% of the threshold")
	readAll(t, env, "c.bin")
	env.FS.SaveUsage()
	env.FS.SaveUsage()
	requireWarnings(t, env, 1, 0, "at 90%")

	if got := readAll(t, env, "d.bin"); !bytes.Equal(got, files["d.bin"]) {
		t.Fatal("a file read past the threshold differs")
	}
	env.FS.SaveUsage()
	env.FS.SaveUsage()
	requireWarnings(t, env, 1, 1, "at 120%")

	env.Restart()
	env.FS.SaveUsage()
	if passed := usageWarnings(env, "passed"); passed != 2 {
		t.Fatalf("after a restart past the threshold the log warns %d times that it passed", passed)
	}

	env.Clock.Advance(25 * time.Hour)
	env.FS.SaveUsage()
	for _, name := range []string{"e.bin", "f.bin", "g.bin"} {
		readAll(t, env, name)
	}
	env.FS.SaveUsage()
	requireWarnings(t, env, 2, 2, "a new climb to 90%")
}

func TestUsageWarnsOfTheUnitsToo(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Tune = func(o *core.Options) { o.FreeUnits = 1 }
	env.Start()
	env.FS.SaveUsage()
	if got := env.Logs("passed the project's free threshold"); len(got) != 1 || !strings.Contains(got[0], "quota units") {
		t.Fatalf("the first load past a threshold of 1 unit warns %q", got)
	}
}

func TestCountAtTheEdgeOfTheWarningStaysNear(t *testing.T) {
	t.Parallel()
	const free = 1000
	level := 0
	for _, step := range []struct {
		n    int64
		want int
	}{
		{850, 0}, {900, 1}, {890, 1}, {910, 1}, {800, 1}, {799, 0}, {890, 0}, {900, 1}, {1001, 2}, {950, 1}, {850, 1},
	} {
		if level = core.UsageLevelAfter(step.n, free, level); level != step.want {
			t.Fatalf("at %d of %d the level is %d, want %d", step.n, free, level, step.want)
		}
	}
}
