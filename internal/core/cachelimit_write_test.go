package core_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestAFailingWriteOfTheCacheLimitWarnsOnceInTenMinutes(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	db := filepath.Join(env.Dir, "meta.db")
	refuse := func() {
		t.Helper()
		for _, on := range []string{"INSERT", "UPDATE"} {
			if err := execSQL(db, `CREATE TRIGGER refuse_limit_`+on+` BEFORE `+on+` ON state
				WHEN NEW.key = 'cache_limit' BEGIN SELECT RAISE(ABORT, 'the disk is full'); END`); err != nil {
				t.Fatal(err)
			}
		}
	}
	refuse()
	warned := func() int { return len(env.Logs("write the cache limit for status")) }
	for range 5 {
		if env.FS.CacheLimitChanged(1<<30, "cache_size") == nil {
			t.Fatal("scenario not built: the write of the limit went through")
		}
		env.Clock.Advance(time.Second)
	}
	if n := warned(); n != 1 {
		t.Fatalf("five failed writes in five seconds warned %d times, want once", n)
	}
	env.Clock.Advance(10 * time.Minute)
	_ = env.FS.CacheLimitChanged(1<<30, "cache_size")
	if n := warned(); n != 2 {
		t.Fatalf("a failed write ten minutes later warned %d times in all, want 2", n)
	}
	if err := execSQL(db, `DROP TRIGGER refuse_limit_INSERT; DROP TRIGGER refuse_limit_UPDATE`); err != nil {
		t.Fatal(err)
	}
	if err := env.FS.CacheLimitChanged(1<<30, "cache_size"); err != nil {
		t.Fatal(err)
	}
	refuse()
	_ = env.FS.CacheLimitChanged(1<<30, "cache_size")
	if n := warned(); n != 3 {
		t.Fatalf("a failed write after one that went through warned %d times in all, want 3", n)
	}
}
