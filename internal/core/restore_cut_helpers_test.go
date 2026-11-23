package core_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func cutMove(t *testing.T, database, mark bool) (env *testenv.Env, earlier, begun string, content []byte) {
	t.Helper()
	env = testenv.New(t)
	env.Start()
	earlier = filepath.Join(env.Dir, "untrusted-state-20200101-000000")
	if err := os.Mkdir(earlier, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(earlier, "meta.db"), []byte("an earlier state"), 0o600); err != nil {
		t.Fatal(err)
	}
	env.Emu.SetOffline(true)
	content = testenv.RandomBytes(3000, 630)
	writeFile(t, env, "", "made.bin", content)
	if err := env.FS.Close(); err != nil {
		t.Fatal(err)
	}
	env.FS = nil
	begun = filepath.Join(env.Dir, "untrusted-state-20260102-030405")
	if err := os.Mkdir(begun, 0o700); err != nil {
		t.Fatal(err)
	}
	moveIntoCopy(t, env.Dir, begun, database)
	if !mark {

		if err := os.WriteFile(filepath.Join(env.Dir, "untrusted"), []byte("cut short\ncopy: "+filepath.Base(begun)+"\n"),
			0o600); err != nil {
			t.Fatal(err)
		}
	}
	return env, earlier, begun, content
}

func moveIntoCopy(t *testing.T, dir, begun string, database bool) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() == filepath.Base(core.ContentDir("")) || database && strings.HasPrefix(e.Name(), "meta.db") {
			if err := os.Rename(filepath.Join(dir, e.Name()), filepath.Join(begun, e.Name())); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func requireMoveCutShortGoesOn(t *testing.T, database, mark bool) {
	t.Helper()
	env, earlier, begun, content := cutMove(t, database, mark)
	env.Emu.SetOffline(false)
	env.Start()
	if copies := stateCopies(t, env); marked(env) || len(copies) != 2 {
		t.Fatalf("after the start: marked %v, copies %q, want the two that were there", marked(env), copies)
	}
	if _, err := os.Lstat(filepath.Join(begun, "meta.db")); err != nil {
		t.Fatalf("the database is not in the copy with its content: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(earlier, "meta.db")); err != nil || string(got) != "an earlier state" {
		t.Fatalf("the earlier copy holds %q: %v", got, err)
	}
	if got := readAll(t, env, "made.bin"); !bytes.Equal(got, content) {
		t.Fatal("the file made here is not back in the mount as it was")
	}
}

func requireRestoreCutAfterStep(t *testing.T, i int, step string) {
	t.Helper()
	env := testenv.New(t)
	content := testenv.RandomBytes(256<<10, 1460)
	env.Emu.WriteFile(env.Emu.Root(), "f.bin", content)
	env.Start()
	changedUnfetched(t, env)
	fh := openWriter(t, env, "f.bin", 0)
	write(t, env, fh, 8192, []byte("edit"))
	env.FS.Release(fh)
	if err := env.FS.Close(); err != nil {
		t.Fatal(err)
	}
	env.FS = nil
	if err := os.WriteFile(filepath.Join(env.Dir, "untrusted"), []byte("for the test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	killed := killAfter(env, step)
	env.Start()
	if !killed.Load() {
		t.Fatal("scenario not built: the restore did not reach the step")
	}
	copies := core.StateCopies(env.Dir)
	if len(copies) != 1 {
		t.Fatalf("the cache directory holds the copies %q", copies)
	}
	if i == 0 {
		requireChangeStaysInCopy(t, env, copies[0])
	}

	if err := env.FS.Store().SetState(meta.StateRestoreFrom, copies[0]); err != nil {
		t.Fatal(err)
	}
	env.Tune = nil
	env.Restart()
	names := lostNames(env, lostDir)
	if len(names) != 1 || string(readAll(t, env, lostDir+"/"+names[0])[8192:8196]) != "edit" || inCopy(copies[0]) {
		t.Fatalf("after the restore went on Lost and Found holds %q, want the change once", names)
	}
}

func inCopy(copyDir string) bool {
	content, _ := filepath.Glob(filepath.Join(core.ContentDir(copyDir), "*", "*"))
	return len(content) > 0
}

func requireChangeStaysInCopy(t *testing.T, env *testenv.Env, copyDir string) {
	t.Helper()
	if _, err := env.Resolve(lostDir); err == nil && len(lostNames(env, lostDir)) != 0 {
		t.Fatalf("Lost and Found holds %q before the row of the change", lostNames(env, lostDir))
	}
	if !inCopy(copyDir) {
		t.Fatal("the change is neither in Lost and Found nor in the copy")
	}
	if len(env.Logs("stays in the copy")) == 0 {
		t.Fatal("the log does not tell that the change stays in the copy")
	}
}
