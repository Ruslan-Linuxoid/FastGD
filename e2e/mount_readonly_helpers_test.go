//go:build e2e

package e2e

import (
	"bytes"
	"crypto/md5"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

func requireListing(t *testing.T, d *daemon) {
	t.Helper()
	if got := dirNames(t, d.path("Photos")); !slices.Equal(got, []string{"2025", "small.txt", "video.bin"}) {
		t.Fatalf("Photos lists %q", got)
	}
}

func requireFind(t *testing.T, d *daemon) {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "find", d.mnt, "-mindepth", "1").Output()
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Fields(strings.ReplaceAll(string(out), d.mnt+"/", ""))
	slices.Sort(got)
	want := []string{"Photos", "Photos/2025", "Photos/small.txt", "Photos/video.bin"}
	if !slices.Equal(got, want) {
		t.Fatalf("find lists %q, want %q", got, want)
	}
}

func requireStat(t *testing.T, d *daemon, size int, mtime, photosMtime time.Time) {
	t.Helper()
	requireFileStat(t, d, size, mtime)
	requireFolderStat(t, d, photosMtime)
}

func requireFileStat(t *testing.T, d *daemon, size int, mtime time.Time) {
	t.Helper()
	fi, err := os.Stat(d.path("Photos/video.bin"))
	if err != nil {
		t.Fatal(err)
	}
	st := fi.Sys().(*syscall.Stat_t)
	if fi.Size() != int64(size) || fi.Mode() != 0o644 || !fi.ModTime().Equal(mtime) || st.Uid != testUID ||
		st.Gid != testGID {
		t.Fatalf("video.bin: size %d mode %v mtime %v uid %d gid %d", fi.Size(), fi.Mode(), fi.ModTime(), st.Uid, st.Gid)
	}
}

func requireFolderStat(t *testing.T, d *daemon, photosMtime time.Time) {
	t.Helper()
	di, err := os.Stat(d.path("Photos"))
	if err != nil {
		t.Fatal(err)
	}
	dst := di.Sys().(*syscall.Stat_t)
	if di.Mode() != fs.ModeDir|0o755 || !di.ModTime().Equal(photosMtime) || dst.Uid != testUID || dst.Gid != testGID {
		t.Fatalf("Photos: mode %v mtime %v uid %d gid %d", di.Mode(), di.ModTime(), dst.Uid, dst.Gid)
	}
}

func requireReadFromMiddle(t *testing.T, e *emu, d *daemon, big []byte) {
	t.Helper()
	e.ResetRequests()
	f, err := os.Open(d.path("Photos/video.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	off := int64(20<<20 + 777)
	buf := make([]byte, 4096)
	if _, err := f.ReadAt(buf, off); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(buf, big[off:off+4096]) {
		t.Fatal("bytes from the middle differ from Drive's")
	}
	var asked int64
	for _, r := range e.Requests() {
		if r.Query.Get("alt") != "media" {
			continue
		}
		if !strings.HasPrefix(r.Range, "bytes=") {
			t.Fatalf("download without a range: %+v", r)
		}
		var from, to int64
		if _, err := fmt.Sscanf(r.Range, "bytes=%d-%d", &from, &to); err != nil {
			t.Fatalf("range %q: %v", r.Range, err)
		}
		asked += to - from + 1
	}
	if asked == 0 || asked > int64(len(big))/2 {
		t.Fatalf("a read of 4 KiB from the middle downloaded %d bytes", asked)
	}
}

func requireReadAnywhere(t *testing.T, d *daemon, big []byte) {
	t.Helper()
	f, err := os.Open(d.path("Photos/video.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, off := range []int64{1, 8<<20 - 100, 33<<20 + 5, int64(len(big)) - 4096} {
		buf := make([]byte, 4096)
		if _, err := f.ReadAt(buf, off); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(buf, big[off:off+4096]) {
			t.Fatalf("bytes at %d differ from Drive's", off)
		}
	}
}

func requireCat(t *testing.T, d *daemon, big []byte) {
	t.Helper()
	got, err := os.ReadFile(d.path("Photos/video.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if md5.Sum(got) != md5.Sum(big) {
		t.Fatal("md5 of the mounted file differs from Drive's")
	}
}
