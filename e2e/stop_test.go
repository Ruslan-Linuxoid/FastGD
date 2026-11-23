//go:build e2e

package e2e

import (
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestStopWaitsForUploadsOnlySoLong(t *testing.T) {
	e := startEmu(t)
	d := newDaemon(t, e)
	d.stopGrace = "3s"
	d.start()
	content := testenv.RandomBytes(100<<20+12<<20, 44)
	stopInTheMiddleOfALargeUpload(t, e, d, content)
	requireTheRestGoesInItsSession(t, e, d, content)

	release := e.HoldUploads()
	if err := os.WriteFile(d.path("small.txt"), []byte("small"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the upload to start", 20*time.Second, func() bool { return e.UploadsWaiting() == 1 })
	time.AfterFunc(time.Second, release)
	d.stop()
	if f, ok := e.ByPath("small.txt"); !ok || string(f.Content) != "small" {
		t.Fatalf("the upload under way at the stop did not end: found %v", ok)
	}
}

func stopInTheMiddleOfALargeUpload(t *testing.T, e *emu, d *daemon, content []byte) {
	t.Helper()

	e.SetUploadRate(4 << 20)
	if err := os.WriteFile(d.path("big.bin"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the first chunk", 30*time.Second, func() bool { return e.SessionBytes() >= 1<<20 })
	start := time.Now()
	d.stop()
	if took := time.Since(start); took < 3*time.Second || took > 10*time.Second {
		t.Fatalf("the stop in the middle of an upload took %v, want the 3 seconds it has and little more", took)
	}
	if _, ok := e.ByPath("big.bin"); ok {
		t.Fatal("scenario not built: the upload ended before the stop")
	}
}

func requireTheRestGoesInItsSession(t *testing.T, e *emu, d *daemon, content []byte) {
	t.Helper()
	e.SetUploadRate(0)
	e.ResetRequests()
	d.start()
	waitInDrive(t, e, content, "big.bin")
	for _, r := range e.Requests() {
		if len(r.Path) >= 8 && r.Path[:8] == "/upload/" && r.Method != http.MethodPut {
			t.Fatalf("after the start the file went otherwise than in its session: %s %s", r.Method, r.Path)
		}
	}
}
