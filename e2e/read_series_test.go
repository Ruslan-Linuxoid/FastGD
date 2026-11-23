//go:build e2e

package e2e

import (
	"bytes"
	"crypto/md5"
	"io"
	"os"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

const seriesFileSize = 128 << 20

const seriesMostRequests = 64

func TestAReadThroughTheMountGrowsTheWindow(t *testing.T) {
	e := startEmu(t)
	content := testenv.RandomBytes(seriesFileSize, 1901)
	id := e.WriteFile(e.Root(), "large.bin", content)
	d := newDaemon(t, e)
	d.start()
	e.ResetRequests()
	f, err := os.Open(d.path("large.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	h := md5.New()

	if _, err := io.CopyBuffer(h, struct{ io.Reader }{f}, make([]byte, 4<<10)); err != nil {
		t.Fatal(err)
	}
	if want := md5.Sum(content); !bytes.Equal(h.Sum(nil), want[:]) {
		t.Fatal("the file read through the mount differs")
	}
	requireFewDownloads(t, e, id)
}

func TestAReadOutOfOrderThroughTheMountGrowsTheWindow(t *testing.T) {
	e := startEmu(t)
	content := testenv.RandomBytes(seriesFileSize, 1902)
	id := e.WriteFile(e.Root(), "large.bin", content)
	d := newDaemon(t, e)
	d.start()
	e.ResetRequests()
	f, err := os.Open(d.path("large.bin"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if err := unix.Fadvise(int(f.Fd()), 0, 0, unix.FADV_RANDOM); err != nil {
		t.Fatal(err)
	}
	const block = 128 << 10
	order := []int{0, 1, 2, 3}
	for i := 4; i+1 < seriesFileSize/block; i += 2 {
		order = append(order, i+1, i)
	}
	if len(order) != seriesFileSize/block {
		t.Fatalf("scenario not built: %d reads for the %d blocks of the file", len(order), seriesFileSize/block)
	}
	buf := make([]byte, block)
	for _, b := range order {
		if _, err := f.ReadAt(buf, int64(b)*block); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(buf, content[b*block:(b+1)*block]) {
			t.Fatalf("block %d read through the mount differs", b)
		}
	}
	requireFewDownloads(t, e, id)
}

func requireFewDownloads(t *testing.T, e *emu, id string) {
	t.Helper()
	if n := mediaRequests(e, id); n == 0 || n > seriesMostRequests {
		t.Fatalf("a read of %d MiB through the mount took %d downloads, want 1 to %d", seriesFileSize>>20, n,
			seriesMostRequests)
	}
}
