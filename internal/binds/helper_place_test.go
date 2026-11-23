package binds

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func requireRefused(t *testing.T, th *testHelper, target, why string) {
	t.Helper()
	err := th.bind(filepath.Join(th.mountPoint, "Work"), target)
	if !errors.Is(err, ErrPlaceTaken) || !strings.Contains(err.Error(), why) || len(th.mounted) != 0 {
		t.Fatalf("bind to %s: %v, mounted %q; want a refusal: %s", target, err, th.mounted, why)
	}
}

func TestTheHelperBindsToNoLink(t *testing.T) {
	t.Parallel()
	th := newTestHelper(t)
	empty := filepath.Join(th.outside, "empty")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(th.outside, "link")
	if err := os.Symlink(empty, link); err != nil {
		t.Fatal(err)
	}
	requireRefused(t, th, link, "not a folder")
}

func TestTheHelperBindsToNoRemovedFolder(t *testing.T) {
	t.Parallel()
	th := newTestHelper(t)
	place := filepath.Join(th.outside, "place")
	th.beforeCheck = func() {
		if err := os.Remove(place); err != nil {
			t.Error(err)
		}
	}
	requireRefused(t, th, place, "removed")
}

func TestTheHelperBindsOnNoMount(t *testing.T) {
	t.Parallel()
	th := newTestHelper(t)
	calls := 0
	th.statx = func(fd int, st *unix.Statx_t) error {
		err := statxPlace(fd, st)
		calls++

		if calls == 2 {
			st.Mnt_id++
		}
		return err
	}
	requireRefused(t, th, filepath.Join(th.outside, "place"), "something is mounted there")
}

func TestTheHelperBindsNothingWhereTheKernelTellsNoMount(t *testing.T) {
	t.Parallel()
	th := newTestHelper(t)
	th.statx = func(fd int, st *unix.Statx_t) error {
		err := statxPlace(fd, st)
		st.Mask &^= unix.STATX_MNT_ID
		st.Mnt_id = 0
		return err
	}
	requireRefused(t, th, filepath.Join(th.outside, "place"), "Linux 5.8")
}
