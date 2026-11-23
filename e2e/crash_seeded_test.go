//go:build e2e

package e2e

import (
	"crypto/md5"
	"strings"
	"testing"
	"time"
)

func TestKillsLoseNothing(t *testing.T) {
	r := newSeededRun(t)
	inDrive := 0
	for round := range seededRounds {
		inDrive += r.round(t, round)
	}
	if inDrive == 0 {
		t.Fatalf("seed %d: no upload went through before any of the kills: the kills hit no upload", r.seed)
	}
	r.requireAllInDriveAndTidy(t)
}

func (r *seededRun) requireAllInDriveAndTidy(t *testing.T) {
	t.Helper()

	for _, name := range r.names {
		f := r.files[name]
		if f == nil {
			continue
		}
		parts := strings.Split(name, "/")
		waitFor(t, "the upload of "+name, 2*time.Minute, func() bool {
			got, ok := r.e.ByPath(parts...)
			return ok && md5.Sum(got.Content) == md5.Sum(f.want)
		})
	}
	waitFor(t, "no dirty file left", time.Minute, func() bool {
		return strings.Contains(status(t, r.d), "dirty files:  0,")
	})
	if out := status(t, r.d); !strings.Contains(out, "lost & found: 0 in") {
		t.Fatalf("seed %d: files went to Lost and Found with nothing changed in Drive:\n%s", r.seed, out)
	}

	waitFor(t, "the cache within its limit", 20*time.Second, func() bool {
		return r.d.contentDU() <= seededCacheLimit+int64(len(r.files))*4096
	})
	r.d.stop()
	r.d.requireCachedIsDrives(r.seed)
}
