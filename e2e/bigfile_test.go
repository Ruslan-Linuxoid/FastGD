//go:build e2e

package e2e

import (
	"bufio"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"math/rand/v2"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
)

const bigFileSize = 2 << 30

const bigFilePeak = 256 << 20

const bigBlock = 1 << 20

func startEmuOnDisk(t *testing.T) *emu {
	t.Helper()
	e := emulator.New(emulator.KeepContentIn(t.TempDir()))
	srv := httptest.NewServer(e)
	t.Cleanup(srv.Close)
	return &emu{Emulator: e, srv: srv}
}

func (d *daemon) peak() int64 {
	d.t.Helper()
	n, ok := memoryOf(d.cmd.Process.Pid, "VmHWM:")
	if !ok {
		d.t.Fatal("no VmHWM in the status of the daemon")
	}
	return n
}

func memoryOf(pid int, key string) (int64, bool) {
	f, err := os.Open(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if kib, ok := strings.CutPrefix(sc.Text(), key); ok {
			n, err := strconv.ParseInt(strings.TrimSpace(strings.TrimSuffix(kib, "kB")), 10, 64)
			return n << 10, err == nil
		}
	}
	return 0, false
}

func (d *daemon) watchMemory(phase string) func() {
	t, pid := d.t, d.cmd.Process.Pid
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		for {
			select {
			case <-done:
				return
			case <-tick.C:
			}
			if n, ok := memoryOf(pid, "VmRSS:"); ok && n > bigFilePeak {
				t.Errorf("the daemon came to hold %d MiB while it %s, above %d", n>>20, phase, bigFilePeak>>20)
				_ = syscall.Kill(pid, syscall.SIGKILL)
				return
			}
		}
	}()
	var once sync.Once
	stop := func() { once.Do(func() { close(done); <-stopped }) }

	t.Cleanup(stop)
	return stop
}

func writeBig(t *testing.T, path string) []byte {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	sum := md5.New()
	rng := rand.NewChaCha8([32]byte{205})
	block := make([]byte, bigBlock)
	for range bigFileSize / bigBlock {
		_, _ = rng.Read(block)
		sum.Write(block)
		if _, err := f.Write(block); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	return sum.Sum(nil)
}

func md5Of(t *testing.T, path string) []byte {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := unix.Fadvise(int(f.Fd()), 0, 0, unix.FADV_DONTNEED); err != nil {
		t.Fatal(err)
	}
	sum := md5.New()
	if _, err := io.CopyBuffer(struct{ hash.Hash }{sum}, f, make([]byte, bigBlock)); err != nil {
		t.Fatal(err)
	}
	return sum.Sum(nil)
}

func TestABigFileGoesThroughWithFlatMemory(t *testing.T) {
	e := startEmuOnDisk(t)
	d := newDaemon(t, e)
	d.start()
	unwatch := d.watchMemory("wrote and uploaded 2 GiB")
	want := writeBig(t, d.path("big.bin"))
	if out, ok := syncNow(t, d); !ok {
		t.Fatalf("sync: %s", out)
	}

	var sum string
	var size int64
	for _, f := range e.All() {
		if f.Name == "big.bin" {
			sum, size, _ = e.Checksum(f.ID)
		}
	}
	if size != bigFileSize || sum != hex.EncodeToString(want) {
		t.Fatalf("Drive holds %d bytes of big.bin of md5 %s; want all of it", size, sum)
	}
	unwatch()
	if p := d.peak(); p > bigFilePeak {
		t.Errorf("the daemon held %d MiB at its peak while it wrote and uploaded 2 GiB, want at most %d", p>>20,
			bigFilePeak>>20)
	} else {
		t.Logf("the peak of the daemon while it wrote and uploaded 2 GiB: %d MiB", p>>20)
	}

	d.stop()
	if err := os.RemoveAll(d.cache); err != nil {
		t.Fatal(err)
	}
	d.start()
	unwatch = d.watchMemory("downloaded and read 2 GiB")
	if got := md5Of(t, d.path("big.bin")); string(got) != string(want) {
		t.Fatal("the file read back from Drive is not the one written")
	}
	unwatch()
	if p := d.peak(); p > bigFilePeak {
		t.Errorf("the daemon held %d MiB at its peak while it downloaded and read 2 GiB, want at most %d", p>>20,
			bigFilePeak>>20)
	} else {
		t.Logf("the peak of the daemon while it downloaded and read 2 GiB: %d MiB", p>>20)
	}
}
