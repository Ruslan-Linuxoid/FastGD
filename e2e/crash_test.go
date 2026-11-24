//go:build e2e

package e2e

import (
	"bytes"
	"database/sql"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/Ruslan-Linuxoid/FastGD/internal/cache"
	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

const crashSeedEnv = "E2E_CRASH_SEED"

type crashFile struct {
	want []byte

	maybe []byte

	made bool
}

func (d *daemon) kill() {
	d.t.Helper()
	_ = d.cmd.Process.Signal(syscall.SIGKILL)
	<-d.done
	_ = syscall.Unmount(d.mnt, syscall.MNT_DETACH)
}

func (d *daemon) contentDU() int64 {
	var n int64
	_ = filepath.WalkDir(core.ContentDir(d.cache), func(p string, e fs.DirEntry, err error) error {
		if err != nil || e.IsDir() {
			return nil
		}
		var st syscall.Stat_t
		if syscall.Stat(p, &st) == nil {
			n += st.Blocks * 512
		}
		return nil
	})
	return n
}

func (d *daemon) requireCachedIsDrives(seed uint64) {
	d.t.Helper()
	db, err := sql.Open("sqlite", "file:"+filepath.Join(d.cache, "meta.db")+"?mode=ro")
	if err != nil {
		d.t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.QueryContext(d.t.Context(),
		`SELECT i.id, p.off, p.len FROM pieces p JOIN items i ON i.ino = p.ino WHERE i.dirty = 0`)
	if err != nil {
		d.t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var off, n int64
		if err := rows.Scan(&id, &off, &n); err != nil {
			d.t.Fatal(err)
		}
		d.requirePieceIsDrives(seed, id, off, n)
	}
	if err := rows.Err(); err != nil {
		d.t.Fatal(err)
	}
}

func (d *daemon) requirePieceIsDrives(seed uint64, id string, off, n int64) {
	d.t.Helper()
	in, ok := d.emu.Get(id)
	if !ok || off+n > int64(len(in.Content)) {
		d.t.Fatalf("seed %d: the database records a piece of %s at %d+%d that Drive has no bytes for", seed, id, off, n)
	}
	fd, err := os.Open(cache.PathIn(core.ContentDir(d.cache), id))
	if err != nil {
		d.t.Fatalf("seed %d: a piece of %s is recorded and its file is not there: %v", seed, id, err)
	}
	got := make([]byte, n)
	_, err = fd.ReadAt(got, off)
	fd.Close()
	if err != nil || !bytes.Equal(got, in.Content[off:off+n]) {
		d.t.Fatalf("seed %d: the piece of %s at %d+%d recorded as cached does not hold Drive's bytes: %v", seed, in.Name,
			off, n, err)
	}
}

func TestKillInAMoveIntoLostAndFoundLosesNothing(t *testing.T) {
	for _, step := range core.ShelveSteps() {
		t.Run(step, func(t *testing.T) { killAfterTheStepOfAMoveIntoLostAndFound(t, step) })
	}
}

func killAfterTheStepOfAMoveIntoLostAndFound(t *testing.T, step string) {
	t.Helper()
	e := startEmu(t)
	content := testenv.RandomBytes(300<<10, 90)
	id := e.WriteFile(e.Mkdir(e.Root(), "docs"), "a.bin", content)
	d := newDaemon(t, e)
	d.dieAfter = step
	d.start()
	changed, want := editWhileDriveChanges(t, e, d, id, content)
	waitFor(t, "the daemon to kill itself after the step", 2*time.Minute, func() bool { return !d.running() })
	d.kill()
	d.dieAfter = ""
	d.start()
	lost := d.path("/Lost and Found (FastGD)/My Drive/docs")
	waitFor(t, "the local version in Lost and Found", 2*time.Minute, func() bool {
		names, _ := filepath.Glob(lost + "/a (конфликт *).bin")
		if len(names) != 1 {
			return false
		}
		got, err := os.ReadFile(names[0])
		return err == nil && bytes.Equal(got, want)
	})
	if got, err := os.ReadFile(d.path("docs/a.bin")); err != nil || !bytes.Equal(got, changed) {
		t.Fatalf("the file does not read as the version put in Drive: %v", err)
	}
	if got, _ := e.Get(id); !bytes.Equal(got.Content, changed) {
		t.Fatal("the edit went over the version put in Drive after it")
	}
}

func editWhileDriveChanges(t *testing.T, e *emu, d *daemon, id string, content []byte) (changed, want []byte) {
	t.Helper()
	f, err := os.OpenFile(d.path("docs/a.bin"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadAll(f); err != nil {
		t.Fatal(err)
	}
	patch := testenv.RandomBytes(1<<10, 91)
	if _, err := f.WriteAt(patch, 100<<10); err != nil {
		t.Fatal(err)
	}
	changed = testenv.RandomBytes(50<<10, 92)
	e.Update(id, func(f *emulator.File) { f.Content = changed })
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	want = append([]byte(nil), content...)
	copy(want[100<<10:], patch)
	return changed, want
}

func TestKillWhileGivingWayToDriveLeavesTheFileReadable(t *testing.T) {
	e := startEmu(t)
	id := e.WriteFile(e.Root(), "a.bin", testenv.RandomBytes(300<<10, 1470))
	d := newDaemon(t, e)
	d.dieAfter = core.TakeRemoteStep
	d.start()
	f, err := os.OpenFile(d.path("a.bin"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte("edit"), 100); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(d.path("a.bin")); err != nil {
		t.Fatal(err)
	}
	theirs := testenv.RandomBytes(50<<10, 1471)
	e.Update(id, func(f *emulator.File) { f.Content = theirs })
	waitFor(t, "the daemon to kill itself after the step", 2*time.Minute, func() bool { return !d.running() })
	d.kill()
	d.dieAfter = ""
	d.start()
	waitFor(t, "the file to read as Drive's version", 2*time.Minute, func() bool {
		got, err := os.ReadFile(d.path("a.bin"))
		return err == nil && bytes.Equal(got, theirs)
	})
}

func requireKillInRestoreLosesNothing(t *testing.T, step string) {
	t.Helper()
	e := startEmu(t)
	content := testenv.RandomBytes(300<<10, 1472)
	id := e.WriteFile(e.Root(), "a.bin", content)
	d := newDaemon(t, e)
	d.start()
	f, err := os.OpenFile(d.path("a.bin"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteAt([]byte("edit"), 100); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	d.kill()
	theirs := testenv.RandomBytes(50<<10, 1473)
	e.Update(id, func(f *emulator.File) { f.Content = theirs })
	if err := os.WriteFile(filepath.Join(d.cache, "untrusted"), []byte("for the test\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	d.dieAfter = step
	cmd := d.command()
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err == nil || !strings.Contains(out.String(), "kills itself") {
		t.Fatalf("the daemon did not kill itself in the restore (%v):\n%s", err, out.String())
	}
	d.dieAfter = ""
	d.start()
	want := append([]byte(nil), content...)
	copy(want[100:], "edit")
	names := waitChangeInLostAndFound(t, d, want)
	if len(names) != 1 {
		t.Fatalf("Lost and Found holds %q, want the change once", names)
	}
	if got, err := os.ReadFile(d.path("a.bin")); err != nil || !bytes.Equal(got, theirs) {
		t.Fatalf("the file does not read as Drive's version: %v", err)
	}
}

func waitChangeInLostAndFound(t *testing.T, d *daemon, want []byte) []string {
	t.Helper()
	var names []string
	waitFor(t, "the change in Lost and Found", 2*time.Minute, func() bool {
		names, _ = filepath.Glob(d.path("/Lost and Found (FastGD)/My Drive") + "/a (конфликт *).bin")
		for _, n := range names {
			if got, err := os.ReadFile(n); err == nil && bytes.Equal(got, want) {
				return true
			}
		}
		return false
	})
	return names
}

func TestKillInARestoreIntoLostAndFoundLosesNothing(t *testing.T) {
	for _, step := range core.RestoreSteps() {
		t.Run(step, func(t *testing.T) {
			requireKillInRestoreLosesNothing(t, step)
		})
	}
}
