//go:build e2e

package e2e

import (
	"crypto/md5"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func duBytes(t *testing.T, dir string) int64 {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "du", "-s", "-B1", dir).Output()
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.ParseInt(strings.Fields(string(out))[0], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestOnlyWhatIsReadIsCachedOnDisk(t *testing.T) {
	e := startEmu(t)
	const size = 20<<20 + 999
	content := testenv.RandomBytes(size, 11)
	e.WriteFile(e.Root(), "movie.bin", content)
	e.WriteFile(e.Root(), "other.bin", testenv.RandomBytes(1<<20, 12))
	d := newDaemon(t, e)
	d.start()
	stored := core.ContentDir(d.cache)

	readTheHeadAndTheTail(t, d, size)
	waitFor(t, "the head and the tail to land", 5*time.Second, func() bool { return duBytes(t, stored) >= 2*4096 })
	time.Sleep(100 * time.Millisecond)
	if du := duBytes(t, stored); du > 4*4096+2*4096 {
		t.Fatalf("after reading the head and the tail the cache takes %d bytes", du)
	}

	got, err := os.ReadFile(d.path("movie.bin"))
	if err != nil || md5.Sum(got) != md5.Sum(content) {
		t.Fatalf("full read: %v", err)
	}
	if du := duBytes(t, stored); du < size || du > size+(1<<20) {
		t.Fatalf("after a full read the cache takes %d bytes for a %d byte file", du, size)
	}

	requireCachedFileReadsAfterRestartWithDriveDown(t, e, d, content)
}

func readTheHeadAndTheTail(t *testing.T, d *daemon, size int64) {
	t.Helper()

	f, err := os.Open(d.path("movie.bin"))
	if err != nil {
		t.Fatal(err)
	}
	head := make([]byte, 4096)
	if _, err := f.ReadAt(head, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := f.ReadAt(make([]byte, 4096), size-4096); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

func requireCachedFileReadsAfterRestartWithDriveDown(t *testing.T, e *emu, d *daemon, content []byte) {
	t.Helper()
	d.stop()
	e.SetOffline(true)
	d.start()
	got, err := os.ReadFile(d.path("movie.bin"))
	if err != nil || md5.Sum(got) != md5.Sum(content) {
		t.Fatalf("read after restart with Drive down: %v", err)
	}
	if _, err := os.ReadFile(d.path("other.bin")); err == nil {
		t.Fatal("uncached file read with Drive down")
	}
}
