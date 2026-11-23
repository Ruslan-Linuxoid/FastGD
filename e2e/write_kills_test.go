//go:build e2e

package e2e

import (
	"bytes"
	"crypto/md5"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestUploadCutByKillGoesUpAfterRestart(t *testing.T) {
	e := startEmu(t)
	d := newDaemon(t, e)
	d.start()
	release := e.HoldUploads()
	content := testenv.RandomBytes(1<<20, 41)
	if err := os.WriteFile(d.path("f.bin"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the upload to start", 20*time.Second, func() bool { return e.UploadsWaiting() == 1 })
	if err := d.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-d.done

	release()
	waitFor(t, "the held request to end", 5*time.Second, func() bool { return e.UploadsWaiting() == 0 })
	e.ResetRequests()

	d.start()
	if got, err := os.ReadFile(d.path("f.bin")); err != nil || !bytes.Equal(got, content) {
		t.Fatalf("after the restart f.bin reads wrong: %v", err)
	}
	waitFor(t, "an upload after the restart", 20*time.Second, func() bool {
		for _, r := range e.Requests() {
			if strings.HasPrefix(r.Path, "/upload/drive/v3/files") {
				return true
			}
		}
		return false
	})
	waitInDrive(t, e, content, "f.bin")
}

func TestKilledResumableUploadGoesOn(t *testing.T) {
	e := startEmu(t)
	d := newDaemon(t, e)
	d.start()
	e.SetUploadRate(4 << 20)
	content := testenv.RandomBytes(100<<20+12<<20, 42)
	if err := os.WriteFile(d.path("big.bin"), content, 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the first chunk", 30*time.Second, func() bool { return e.SessionBytes() >= 1<<20 })
	if err := d.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-d.done

	held := e.SessionBytes()
	e.SetUploadRate(0)
	e.ResetRequests()

	d.start()
	waitInDrive(t, e, content, "big.bin")
	first := ""
	for _, r := range e.Requests() {
		if !strings.HasPrefix(r.Path, "/upload/") {
			continue
		}
		if r.Method != http.MethodPut {

			t.Fatalf("after the restart the file went otherwise than in its session: %s %s?%s", r.Method, r.Path,
				r.Query.Encode())
		}
		if first == "" && !strings.HasPrefix(r.ContentRange, "bytes */") {
			first = r.ContentRange
		}
	}
	var from int64
	if _, err := fmt.Sscanf(first, "bytes %d-", &from); err != nil || from < held {
		t.Fatalf("after the restart the first chunk went as %q, want it to go on at %d or further, where Drive holds the "+
			"session: %v", first, held, err)
	}
}

func TestKilledEditOfAFileFromDriveGoesUp(t *testing.T) {
	e := startEmu(t)
	content := testenv.RandomBytes(2<<20+100, 85)
	e.WriteFile(e.Root(), "edit.bin", content)
	d := newDaemon(t, e)
	d.start()
	release := e.HoldUploads()
	f, err := os.OpenFile(d.path("edit.bin"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	want := append([]byte(nil), content...)
	if _, err := f.WriteAt([]byte("edit"), 1<<20+10); err != nil {
		t.Fatal(err)
	}
	copy(want[1<<20+10:], "edit")
	if _, err := f.WriteAt([]byte("tail"), int64(len(content))); err != nil {
		t.Fatal(err)
	}
	want = append(want, "tail"...)
	f.Close()
	if err := d.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-d.done
	release()
	d.start()
	if got, err := os.ReadFile(d.path("edit.bin")); err != nil || md5.Sum(got) != md5.Sum(want) {
		t.Fatalf("after kill -9 the edited file reads different: %v", err)
	}
	waitInDrive(t, e, want, "edit.bin")
}

func TestRemovalAfterAKilledCreateReachesTheTrash(t *testing.T) {
	e := startEmu(t)
	d := newDaemon(t, e)
	d.start()
	release := e.HoldUploads()
	if err := os.WriteFile(d.path("f.bin"), []byte("content"), 0o644); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the create to reach Drive", 20*time.Second, func() bool { return e.UploadsWaiting() == 1 })
	if err := d.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-d.done

	release()
	waitFor(t, "the held request to end", 5*time.Second, func() bool { return e.UploadsWaiting() == 0 })
	d.start()
	if err := os.Remove(d.path("f.bin")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the file to reach Drive's trash", 30*time.Second, func() bool {
		for _, f := range e.All() {
			if f.Name == "f.bin" {
				return f.Trashed
			}
		}
		return false
	})

	polls := func() int {
		n := 0
		for _, r := range e.Requests() {
			if r.Path == "/drive/v3/changes" {
				n++
			}
		}
		return n
	}
	before := polls()
	waitFor(t, "two more polls", time.Minute, func() bool { return polls() >= before+2 })
	if _, err := os.Stat(d.path("f.bin")); !os.IsNotExist(err) {
		t.Fatalf("the removed file is back in the mount: %v", err)
	}
}
