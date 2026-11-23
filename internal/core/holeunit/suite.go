package holeunit

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"syscall"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/cache"
	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func Run(t *testing.T, unit, portion int64) {
	t.Helper()
	t.Run("the log tells the unit and the portion", func(t *testing.T) {
		t.Parallel()
		env, _ := start(t, unit, 1000, 899)
		if got := cache.Portion(); got != portion {
			t.Fatalf("the working copies put %d bytes at a time, want %d", got, portion)
		}
		if len(env.Logs(fmt.Sprintf("unit=%d portion=%d", unit, portion))) == 0 {
			t.Fatalf("the log does not tell the unit %d and the portion %d", unit, portion)
		}
	})
	t.Run("a write into a part of a unit fetches the unit", func(t *testing.T) {
		t.Parallel()
		writeIntoAPartOfAUnitFetchesTheUnit(t, unit)
	})
	t.Run("the first write counts by units", func(t *testing.T) {
		t.Parallel()
		firstWriteCountsByUnits(t, unit)
	})
	t.Run("a unit that is no multiple of the file system's is refused", unitThatIsNoMultipleOfTheFileSystemsIsRefused)
}

func start(t *testing.T, unit int64, size int, seed uint64) (*testenv.Env, []byte) {
	t.Helper()
	env := testenv.New(t)
	env.Tune = func(o *core.Options) { o.HoleUnit = unit }
	content := testenv.RandomBytes(size, seed)
	env.Emu.WriteFile(env.Emu.Root(), "f.bin", content)
	env.Start()
	if got := cache.HoleUnit(); got != unit {
		t.Fatalf("the cache counts by %d bytes, want the %d asked for", got, unit)
	}
	env.Emu.ResetRequests()
	return env, content
}

func ranges(env *testenv.Env) [][2]int64 {
	var out [][2]int64
	for _, r := range env.Emu.Requests() {
		if r.Query.Get("alt") != "media" {
			continue
		}
		var from, to int64
		if _, err := fmt.Sscanf(r.Range, "bytes=%d-%d", &from, &to); err == nil {
			out = append(out, [2]int64{from, to + 1})
		}
	}
	return out
}

func write(t *testing.T, env *testenv.Env, name string, off int64, data []byte) {
	t.Helper()
	a, err := env.Resolve(name)
	if err != nil {
		t.Fatal(err)
	}
	fh, err := env.FS.Open(context.Background(), a.Ino, syscall.O_RDWR)
	if err != nil {
		t.Fatal(err)
	}
	defer env.FS.Release(fh)
	if n, err := env.FS.Write(context.Background(), fh, off, data); err != nil || n != len(data) {
		t.Fatalf("write %d at %d: %d, %v", len(data), off, n, err)
	}
}

func upload(t *testing.T, env *testenv.Env) []byte {
	t.Helper()
	if left, err := env.FS.SyncNow(context.Background()); err != nil || left != 0 {
		t.Fatalf("the sync left %d operations: %v", left, err)
	}
	f, ok := env.Emu.ByPath("f.bin")
	if !ok {
		t.Fatal("Drive has no f.bin")
	}
	return f.Content
}

func writeIntoAPartOfAUnitFetchesTheUnit(t *testing.T, unit int64) {
	t.Helper()
	env, content := start(t, unit, int(5*unit+1000), 900)
	patch := testenv.RandomBytes(8192, 903)
	write(t, env, "f.bin", 2*unit+4096, patch)

	if covered := coveredFromStart(ranges(env)); covered < int64(len(content)) {
		t.Fatalf("the downloads %v cover the file up to %d of %d bytes, want all of it", ranges(env), covered, len(content))
	}
	want := bytes.Clone(content)
	copy(want[2*unit+4096:], patch)
	if sent := upload(t, env); !bytes.Equal(sent, want) {
		t.Fatalf("Drive holds %d bytes that differ from its version with the write", len(sent))
	}
	requireInUnitGrid(t, ranges(env), unit, int64(len(content)))
}

func coveredFromStart(rs [][2]int64) int64 {
	var covered int64
	for {
		grew := false
		for _, r := range rs {
			if r[0] <= covered && r[1] > covered {
				covered, grew = r[1], true
			}
		}
		if !grew {
			return covered
		}
	}
}

func requireInUnitGrid(t *testing.T, rs [][2]int64, unit, size int64) {
	t.Helper()
	for _, r := range rs {
		if r[0]%unit != 0 || r[1]%unit != 0 && r[1] != size {
			t.Fatalf("the download %v does not lie in the grid of units of %d bytes", r, unit)
		}
	}
}

func firstWriteCountsByUnits(t *testing.T, unit int64) {
	t.Helper()
	env, content := start(t, unit, int(5*unit+1000), 901)
	patch := testenv.RandomBytes(int(unit+10), 902)

	write(t, env, "f.bin", unit, patch)
	for _, r := range ranges(env) {
		if r[0]%unit != 0 || r[0] < 2*unit && r[1] > unit {
			t.Fatalf("the first write downloaded %v, want whole units and none of the unit it covers", r)
		}
	}
	want := bytes.Clone(content)
	copy(want[unit:], patch)
	if sent := upload(t, env); !bytes.Equal(sent, want) {
		t.Fatal("Drive holds other bytes than its version with the write")
	}
}

func unitThatIsNoMultipleOfTheFileSystemsIsRefused(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Tune = func(o *core.Options) { o.HoleUnit = 6000 }
	if _, err := env.Open(); err == nil || !strings.Contains(err.Error(), "no multiple") {
		t.Fatalf("a unit of 6000 bytes got %v, want it refused", err)
	}
}
