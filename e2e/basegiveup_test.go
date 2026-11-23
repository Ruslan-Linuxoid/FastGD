//go:build e2e

package e2e

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/cache"
	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestKillInTheMoveOfABaseGivenUpLosesNoBase(t *testing.T) {
	e := startEmu(t)
	content := testenv.RandomBytes(256<<10, 1542)
	id := e.WriteFile(e.Root(), "a.bin", content)
	d := newDaemon(t, e)
	d.start()
	d.stop()

	patch := testenv.RandomBytes(4096, 1543)
	leaveFileChangedWithNoBase(t, d, id, content, patch)
	want := append([]byte(nil), content...)
	copy(want[8192:], patch)
	e.FailMedia(http.StatusForbidden, "cannotDownloadFile")
	d.dieAfter = core.ShelveSteps()[1]
	d.start()
	waitFor(t, "the daemon to kill itself after the step", 2*time.Minute, func() bool { return !d.running() })
	d.kill()
	e.FailMedia(0, "")
	d.dieAfter = ""
	d.start()
	var lost []string
	waitFor(t, "the edit in Lost and Found or in Drive", 2*time.Minute, func() bool {
		lost, _ = filepath.Glob(d.path("Lost and Found (FastGD)") + "/a (конфликт *).bin")
		got, _ := e.Get(id)
		return len(lost) > 0 || !bytes.Equal(got.Content, content)
	})
	if len(lost) > 0 {
		got, err := os.ReadFile(lost[0])
		if err != nil || !bytes.Equal(got[8192:8192+4096], patch) {
			t.Fatalf("the copy in Lost and Found lacks the edit: %v", err)
		}
		return
	}
	if got, _ := e.Get(id); !bytes.Equal(got.Content, want) {
		t.Fatal("the edit reached Drive without its whole base")
	}
}

func leaveFileChangedWithNoBase(t *testing.T, d *daemon, id string, content, patch []byte) {
	t.Helper()
	dir := filepath.Dir(cache.PathIn(core.ContentDir(d.cache), id))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	fd, err := os.OpenFile(filepath.Join(dir, id), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := fd.Truncate(int64(len(content))); err != nil {
		t.Fatal(err)
	}
	if _, err := fd.WriteAt(patch, 8192); err != nil {
		t.Fatal(err)
	}
	if err := fd.Close(); err != nil {
		t.Fatal(err)
	}
	d.inDB(fmt.Sprintf(`UPDATE items SET dirty = 1, base = %d, base_tail = 0 WHERE id = '%s'`, len(content), id))
}
