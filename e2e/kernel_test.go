//go:build e2e

package e2e

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/cache"
	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func (d *daemon) inDB(queries ...string) {
	d.t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(d.cache, "meta.db"))
	if err != nil {
		d.t.Fatal(err)
	}
	defer db.Close()
	for _, q := range queries {
		if _, err := db.ExecContext(d.t.Context(), q); err != nil {
			d.t.Fatal(q, err)
		}
	}
}

func failing(status, path string) bool { return strings.Contains(status, "\n  My Drive/"+path+"\n") }

func TestUndoneChangesShowOnOpenFiles(t *testing.T) {
	e := startEmu(t)
	e.WriteFile(e.Root(), "timed.txt", []byte("drive's"))
	id := e.WriteFile(e.Root(), "edited.txt", []byte("drive's"))
	d := newDaemon(t, e)
	d.start()

	timed, w, edited, was := openTheTimedAndTheEditedFile(t, d)
	e.Update(id, func(f *emulator.File) { f.CannotModify = true })
	waitFor(t, "the mode of a file its owner made read-only", 30*time.Second, func() bool {
		return permOf(t, edited) == 0o444
	})

	e.SetUploadFault(func(string) int { return 400 })
	setTheTimeOfTheOpenFile(t, d, timed, was)
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	waitFor(t, "Drive to refuse both changes", time.Minute, func() bool {
		out := status(t, d)
		return failing(out, "timed.txt") && failing(out, "edited.txt")
	})
	waitFor(t, "both changes to be undone", 3*time.Minute, func() bool {
		out := status(t, d)
		return strings.Contains(out, "lost & found: 1 ") && !failing(out, "timed.txt") && !failing(out, "edited.txt")
	})

	waitFor(t, "Drive's time on the open file", 5*time.Second, func() bool {
		fi, err := timed.Stat()
		return err == nil && fi.ModTime().Equal(was.ModTime())
	})
	waitFor(t, "the mode of the local version on the open file", 5*time.Second, func() bool {
		return permOf(t, edited) == 0o644
	})
}

func openTheTimedAndTheEditedFile(t *testing.T, d *daemon) (timed, w, edited *os.File, was os.FileInfo) {
	t.Helper()
	timed, err := os.Open(d.path("timed.txt"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { timed.Close() })
	was, err = timed.Stat()
	if err != nil {
		t.Fatal(err)
	}
	w, err = os.OpenFile(d.path("edited.txt"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.WriteAt([]byte("mine"), 0); err != nil {
		t.Fatal(err)
	}
	edited, err = os.Open(d.path("edited.txt"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { edited.Close() })
	return timed, w, edited, was
}

func permOf(t *testing.T, f *os.File) os.FileMode {
	t.Helper()
	fi, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	return fi.Mode().Perm()
}

func setTheTimeOfTheOpenFile(t *testing.T, d *daemon, timed *os.File, was os.FileInfo) {
	t.Helper()
	set := was.ModTime().Add(-48 * time.Hour)
	if err := os.Chtimes(d.path("timed.txt"), set, set); err != nil {
		t.Fatal(err)
	}
	fi, err := timed.Stat()
	if err != nil || !fi.ModTime().Equal(set) {
		t.Fatalf("scenario not built: after the program set the time the open file shows %v: %v", fi, err)
	}
}

func TestRightsLoadedAfterAnUpgradeShowThroughTheKernel(t *testing.T) {
	e := startEmu(t)
	e.Put(emulator.File{Name: "theirs.txt", MimeType: "text/plain", Parents: []string{e.Root()},
		Content: []byte("theirs"), CannotModify: true})
	d := newDaemon(t, e)
	d.start()
	d.stop()

	d.inDB(`UPDATE items SET denied = 0`,
		`INSERT OR REPLACE INTO state(key, value) VALUES ('`+meta.StateRightsUnknown+`', '1')`)

	e.SetOffline(true)
	d.start()
	mode := func() os.FileMode {
		fi, err := os.Stat(d.path("theirs.txt"))
		if err != nil {
			t.Fatal(err)
		}
		return fi.Mode().Perm()
	}
	if m := mode(); m != 0o644 {
		t.Fatalf("scenario not built: before the rights are read the file shows the mode %o", m)
	}
	e.SetOffline(false)
	waitFor(t, "the mode of a file the user may only read", time.Minute, func() bool { return mode() == 0o444 })
}

func TestSizeTakenUpAfterTheStartShowsThroughTheKernel(t *testing.T) {
	e := startEmu(t)
	e.SetUploadFault(func(name string) int {
		if name == "f.bin" {
			return 503
		}
		return 0
	})
	d := newDaemon(t, e)
	d.start()
	if err := os.WriteFile(d.path("f.bin"), testenv.RandomBytes(100000, 95), 0o644); err != nil {
		t.Fatal(err)
	}
	d.stop()
	dir, away := hideTheContentAfterAnAppend(t, d)
	d.start()
	size := func() int64 {
		fi, err := os.Stat(d.path("f.bin"))
		if err != nil {
			t.Fatal(err)
		}
		return fi.Size()
	}
	if n := size(); n != 100000 {
		t.Fatalf("scenario not built: before the file is taken up it shows %d bytes", n)
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(away, dir); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the size the content has on the disk", 2*time.Minute, func() bool { return size() == 150000 })
}

func hideTheContentAfterAnAppend(t *testing.T, d *daemon) (dir, away string) {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(d.cache, "meta.db"))
	if err != nil {
		t.Fatal(err)
	}
	var id string
	err = db.QueryRowContext(t.Context(), `SELECT id FROM items WHERE name = 'f.bin'`).Scan(&id)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}

	dir = filepath.Dir(cache.PathIn(core.ContentDir(d.cache), id))
	fd, err := os.OpenFile(filepath.Join(dir, id), os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fd.Write(testenv.RandomBytes(50000, 96)); err != nil {
		t.Fatal(err)
	}
	fd.Close()

	away = filepath.Join(d.dir, "away")
	if err := os.Rename(dir, away); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	return dir, away
}
