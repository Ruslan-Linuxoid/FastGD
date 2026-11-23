package core_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/cache"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestScatteredReadsJoinTouchingPieces(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	content := testenv.RandomBytes(big, 11)
	env.Emu.WriteFile(env.Emu.Root(), "db.sqlite", content)
	env.Emu.WriteFile(env.Emu.Root(), "few.bin", content)
	env.Start()

	db := openFile(t, env, "db.sqlite")
	env.Emu.ResetRequests()
	for i := range 47 {
		off := int64(i) * 16 * kib
		for _, at := range []int64{off, off + 4*kib} {
			if got := mustRead(t, env, db, at, 4*kib); !bytes.Equal(got, content[at:at+4*kib]) {
				t.Fatal("bytes differ from Drive's")
			}
		}

		waitUntil(t, func() bool { return len(mediaRanges(env)) >= 3*(i+1) })
	}

	waitUntil(t, func() bool { return cachedBytes(pieces(t, env, "db.sqlite")) == 47*12*kib })
	ps := pieces(t, env, "db.sqlite")
	if len(ps) > 80 {
		t.Fatalf("a file read in 141 pieces keeps %d records", len(ps))
	}
	requireNoOverlap(t, ps)

	dbItem, fewItem := item(t, env, "db.sqlite"), item(t, env, "few.bin")
	if got := env.Logs("touching pieces are joined"); len(got) == 0 || !strings.Contains(got[0], dbItem.ID) {
		t.Fatalf("no warning names the joined file: %q", got)
	}

	few := openFile(t, env, "few.bin")
	mustRead(t, env, few, 0, 4*kib)
	mustRead(t, env, few, 8*kib, 4*kib)
	waitUntil(t, func() bool { return len(pieces(t, env, "few.bin")) == 2 })
	for _, l := range env.Logs("touching pieces are joined") {
		if strings.Contains(l, fewItem.ID) {
			t.Fatalf("a file of two pieces was joined: %s", l)
		}
	}
}

func TestOnlyWhatWasReadTakesSpace(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Emu.WriteFile(env.Emu.Root(), "movie.bin", testenv.RandomBytes(big, 12))
	env.Start()
	fh := openFile(t, env, "movie.bin")
	mustRead(t, env, fh, 0, 4*kib)
	mustRead(t, env, fh, big-4*kib, 4*kib)
	waitUntil(t, func() bool { return cachedBytes(pieces(t, env, "movie.bin")) == 8*kib })
	it := item(t, env, "movie.bin")
	var st syscall.Stat_t
	if err := syscall.Stat(contentPath(env, it.Item), &st); err != nil {
		t.Fatal(err)
	}
	if used := st.Blocks * 512; used > 8*kib+4*kib {
		t.Fatalf("the head and tail of a %d byte file take %d bytes on disk", big, used)
	}
}

func TestCacheCountsByTheUnitOfHolesItFinds(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	content := testenv.RandomBytes(256*kib, 40)
	env.Emu.WriteFile(env.Emu.Root(), "f.bin", content)
	env.Start()
	if got := cache.HoleUnit(); got != 4096 {
		t.Fatalf("the cache counts by %d bytes, want 4096", got)
	}
	if len(env.Logs("keeps the holes of a file by 4 KiB")) == 0 {
		t.Fatal("the log does not tell the unit of holes")
	}
	readAll(t, env, "f.bin")
	atRest(t, env)
	if cached(t, env, "f.bin") != 256*kib {
		t.Fatal("scenario not built: the file is not cached whole")
	}
	if err := env.FS.Close(); err != nil {
		t.Fatal(err)
	}
	env.FS = nil

	if err := execSQL(filepath.Join(env.Dir, "meta.db"),
		`UPDATE state SET value = '65536' WHERE key = 'hole_unit'`); err != nil {
		t.Fatal(err)
	}
	env.Start()
	if n := cached(t, env, "f.bin"); n != 0 {
		t.Fatalf("after a change of the unit %d bytes of the file are still recorded as cached", n)
	}
	if got := readAll(t, env, "f.bin"); !bytes.Equal(got, content) {
		t.Fatal("the file reads different after its cache was dropped")
	}

	atRest(t, env)
	env.Restart()
	if n := cached(t, env, "f.bin"); n != 256*kib {
		t.Fatalf("a start with the same unit left %d bytes of the file cached", n)
	}
}
