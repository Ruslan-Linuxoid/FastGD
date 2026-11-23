//go:build e2e && realdrive

package e2e

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func sum(b []byte) string {
	s := md5.Sum(b)
	return hex.EncodeToString(s[:])
}

func writeIn(t *testing.T, d *daemon, r *testenv.Real, name string, content []byte) {
	t.Helper()
	if err := os.WriteFile(inFolder(d, r, name), content, 0o644); err != nil {
		t.Fatal(err)
	}
}

func requireReads(t *testing.T, d *daemon, r *testenv.Real, name string, want []byte) {
	t.Helper()
	if got := readPast(t, inFolder(d, r, name)); !bytes.Equal(got, want) {
		t.Fatalf("%s reads %d bytes different from the %d written", name, len(got), len(want))
	}
}

func TestRealAcceptanceWrittenFileReadsBackAndReachesDrive(t *testing.T) {
	r := testenv.RealDrive(t)
	d := newRealDaemon(t, r, startProxy(t))
	d.start()
	content := testenv.RandomBytes(5<<20+123, 900)
	writeIn(t, d, r, "report.bin", content)
	if got, err := os.ReadFile(inFolder(d, r, "report.bin")); err != nil || !bytes.Equal(got, content) {
		t.Fatalf("read back right after the write: %v", err)
	}
	inDrive(t, r, "report.bin", sum(content))
}

func TestRealAcceptanceFilesOutliveARestart(t *testing.T) {
	r := testenv.RealDrive(t)
	p := startProxy(t)
	d := newRealDaemon(t, r, p)
	d.start()
	sent := testenv.RandomBytes(1<<20, 901)
	writeIn(t, d, r, "sent.bin", sent)
	inDrive(t, r, "sent.bin", sum(sent))
	p.setDown(true)
	unsent := testenv.RandomBytes(1<<20, 902)
	writeIn(t, d, r, "unsent.bin", unsent)
	d.stop()
	p.setDown(false)
	d.start()
	requireReads(t, d, r, "sent.bin", sent)
	requireReads(t, d, r, "unsent.bin", unsent)
	inDrive(t, r, "unsent.bin", sum(unsent))
}

func TestRealAcceptanceWorksWithTheNetworkDownAndUploadsWhenItIsBack(t *testing.T) {
	r := testenv.RealDrive(t)
	p := startProxy(t)
	d := newRealDaemon(t, r, p)
	d.start()
	cached := testenv.RandomBytes(1<<20, 903)
	writeIn(t, d, r, "cached.bin", cached)
	inDrive(t, r, "cached.bin", sum(cached))
	p.setDown(true)
	requireReads(t, d, r, "cached.bin", cached)
	written := map[string][]byte{}
	for i := range 3 {
		name := fmt.Sprintf("new%d.bin", i)
		written[name] = testenv.RandomBytes(256<<10+i, uint64(904+i))
		writeIn(t, d, r, name, written[name])
	}
	waitFor(t, "three dirty files with the network down", time.Minute, func() bool {
		return strings.Contains(status(t, d), "dirty files:  3,")
	})
	for name, want := range written {
		requireReads(t, d, r, name, want)
	}
	p.setDown(false)
	for name, want := range written {
		inDrive(t, r, name, sum(want))
	}
	waitFor(t, "no dirty file left", time.Minute, func() bool {
		return strings.Contains(status(t, d), "dirty files:  0,")
	})
}

func TestRealAcceptanceFullCacheEvictsAndFetchesAgain(t *testing.T) {
	const limit = 8 << 20
	r := testenv.RealDrive(t)
	contents := map[string][]byte{}
	for i := range 9 {
		name := fmt.Sprintf("f%d.bin", i)
		contents[name] = testenv.RandomBytes(2<<20, uint64(920+i))
		upload(t, r, name, contents[name])
	}
	d := newRealDaemon(t, r, startProxy(t))
	d.cacheLimit = limit
	d.start()

	for name, want := range contents {
		requireReads(t, d, r, name, want)
	}
	passed := time.Now()

	waitFor(t, "the cache within its limit", 12*time.Second, func() bool {
		return d.contentDU() <= limit+9*4096
	})
	t.Logf("the cache was back within its limit %v after the last read", time.Since(passed).Round(time.Millisecond))
	for name, want := range contents {
		requireReads(t, d, r, name, want)
	}
}

func upload(t *testing.T, r *testenv.Real, name string, content []byte) {
	t.Helper()
	ids, err := r.Drive.GenerateIDs(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Drive.CreateFile(t.Context(), ids[0], name, r.Folder, "application/octet-stream", time.Now(),
		int64(len(content)), func() io.Reader { return bytes.NewReader(content) }); err != nil {
		t.Fatal(err)
	}
}

func TestRealStartWithoutTheNetwork(t *testing.T) {
	r := testenv.RealDrive(t)
	p := startProxy(t)
	d := newRealDaemon(t, r, p)
	p.setDown(true)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	first := exec.CommandContext(ctx, daemonBin(t), "run", "--config", filepath.Join(d.dir, "config.toml"))
	first.Env = d.command().Env
	out, err := first.CombinedOutput()
	t.Logf("a first start without the network ends with %v:\n%s", err, out)
	if err == nil || ctx.Err() != nil {
		t.Fatalf("a first start without the network does not fail within two minutes: %v", err)
	}
	p.setDown(false)
	d.start()
	content := testenv.RandomBytes(64<<10, 960)
	writeIn(t, d, r, "before.bin", content)
	inDrive(t, r, "before.bin", sum(content))
	d.stop()
	p.setDown(true)
	d.start()
	requireReads(t, d, r, "before.bin", content)
}
