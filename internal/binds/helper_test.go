package binds

import (
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/Ruslan-Linuxoid/FastGD/internal/config"
)

type testHelper struct {
	*helper

	mounted   [][2]string
	unmounted []string

	ours map[string]bool

	outside string
}

func newTestHelper(t *testing.T) *testHelper {
	t.Helper()

	mountIn, err := os.MkdirTemp("/dev/shm", "in")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(mountIn) })
	mnt := config.MountFolder(mountIn, "you@example.com")
	if err := os.MkdirAll(filepath.Join(mnt, "Work", "Docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	th := &testHelper{ours: map[string]bool{mnt: true}, outside: t.TempDir()}
	var in, out unix.Stat_t
	if unix.Stat(mnt, &in) != nil || unix.Stat(th.outside, &out) != nil || in.Dev == out.Dev {
		t.Fatalf("the stand needs /dev/shm and %s on file systems of their own", th.outside)
	}
	th.helper = &helper{
		mountIn:    mountIn,
		mountPoint: mnt,
		statx:      statxPlace,
		typesAt: func(dir string) ([]string, error) {
			if th.ours[dir] {
				return []string{MountType}, nil
			}
			return nil, nil
		},
		mount: func(source, target string) error {

			folder, err := os.Readlink(source)
			if err != nil {
				return err
			}
			place, err := os.Readlink(target)
			if err != nil {
				place = target
			}
			th.mounted = append(th.mounted, [2]string{folder, place})
			return nil
		},
		unmount: func(target string) error {
			th.unmounted = append(th.unmounted, target)
			return nil
		},
	}
	return th
}

func TestTheHelperBindsAFolderOfTheMount(t *testing.T) {
	t.Parallel()
	th := newTestHelper(t)
	outside := th.outside
	empty := filepath.Join(outside, "empty")
	if err := os.Mkdir(empty, 0o755); err != nil {
		t.Fatal(err)
	}
	made := filepath.Join(outside, "made")
	for _, target := range []string{empty, made} {
		if err := th.bind(filepath.Join(th.mountPoint, "Work/Docs"), target); err != nil {
			t.Fatalf("bind to %s: %v", target, err)
		}
	}
	if fi, err := os.Stat(made); err != nil || !fi.IsDir() {
		t.Fatalf("the new place is not made: %v", err)
	}
	want := filepath.Join(th.mountPoint, "Work/Docs")
	if len(th.mounted) != 2 || th.mounted[0] != [2]string{want, empty} || th.mounted[1] != [2]string{want, made} {
		t.Fatalf("mounted %q", th.mounted)
	}
}

func TestTheHelperBindsNothingFromOutsideTheMount(t *testing.T) {
	t.Parallel()
	th := newTestHelper(t)
	outside := th.outside
	secret := filepath.Join(outside, "secret")
	if err := os.Mkdir(secret, 0o700); err != nil {
		t.Fatal(err)
	}

	beside := th.mountPoint + "-beside"
	if err := os.Mkdir(beside, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(th.mountPoint, "Work", "out")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{secret, beside, link, th.mountPoint + "/../secret"} {
		if err := th.bind(source, filepath.Join(outside, "place")); err == nil {
			t.Fatalf("%s was bound", source)
		}
	}
	delete(th.ours, th.mountPoint)
	if err := th.bind(filepath.Join(th.mountPoint, "Work"), filepath.Join(outside, "place")); err == nil {
		t.Fatal("a folder was bound while the mount is not up")
	}
	if len(th.mounted) != 0 {
		t.Fatalf("mounted %q", th.mounted)
	}
}

func TestTheHelperBindsOnlyToAFreePlace(t *testing.T) {
	t.Parallel()
	th := newTestHelper(t)
	outside := th.outside
	full := filepath.Join(outside, "full")
	if err := os.MkdirAll(filepath.Join(full, "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(outside, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(outside, "inside")
	if err := os.Symlink(filepath.Join(th.mountPoint, "Work"), inside); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{full, file, filepath.Join(outside, "none", "place"), "relative",
		filepath.Join(th.mountPoint, "Work", "in"), filepath.Join(inside, "in"), filepath.Join(outside, "x") + "/.",
		filepath.Join(outside, "y") + "/"} {
		err := th.bind(filepath.Join(th.mountPoint, "Work"), target)
		if !errors.Is(err, ErrPlaceTaken) {
			t.Fatalf("bind to %s: %v", target, err)
		}
	}
	if len(th.mounted) != 0 {
		t.Fatalf("mounted %q", th.mounted)
	}
}

func TestTheHelperUnbindsOnlyAMountOfTheDaemon(t *testing.T) {
	t.Parallel()
	th := newTestHelper(t)
	place := filepath.Join(th.outside, "place")
	if err := th.unbind(place); err == nil {
		t.Fatal("a place with no mount of the daemon was unbound")
	}
	if err := th.unbind(th.mountPoint); err == nil {
		t.Fatal("the mount itself was unbound")
	}
	th.ours[place] = true
	if err := th.unbind(place); err != nil || len(th.unmounted) != 1 || th.unmounted[0] != place {
		t.Fatalf("unbind: %v, unmounted %q", err, th.unmounted)
	}
}

func TestTheHelperAnswersUntilTheDaemonCloses(t *testing.T) {
	t.Parallel()
	th := newTestHelper(t)
	daemon, conn := net.Pipe()
	served := make(chan error, 1)
	go func() { served <- th.serve(conn) }()
	enc, dec := json.NewEncoder(daemon), json.NewDecoder(daemon)
	place := filepath.Join(th.outside, "place")
	for _, c := range []struct {
		req  request
		fail string
	}{
		{request{Source: filepath.Join(th.mountPoint, "Work"), Target: place}, ""},
		{request{Target: place}, "nothing of the daemon is mounted"},
	} {
		if err := enc.Encode(c.req); err != nil {
			t.Fatal(err)
		}
		var rep reply
		if err := dec.Decode(&rep); err != nil {
			t.Fatal(err)
		}
		if c.fail == "" && rep.Error != "" || c.fail != "" && !strings.Contains(rep.Error, c.fail) {
			t.Fatalf("%+v answered %q", c.req, rep.Error)
		}
	}
	daemon.Close()
	if err := <-served; err != nil {
		t.Fatalf("the helper ended with %v", err)
	}
}

func TestTheHelperBindsNothingSwappedAfterTheWalk(t *testing.T) {
	t.Parallel()
	th := newTestHelper(t)
	secret := filepath.Join(th.outside, "secret")
	if err := os.Mkdir(secret, 0o700); err != nil {
		t.Fatal(err)
	}
	docs := filepath.Join(th.mountPoint, "Work", "Docs")
	th.beforeOpen = func() {
		if err := os.Rename(docs, docs+".was"); err != nil {
			t.Error(err)
		}
		if err := os.Symlink(secret, docs); err != nil {
			t.Error(err)
		}
	}
	if err := th.bind(docs, filepath.Join(th.outside, "place")); err == nil {
		t.Fatal("a folder swapped after the walk was bound")
	}
	if len(th.mounted) != 0 {
		t.Fatalf("mounted %q", th.mounted)
	}
}

func TestTheHelperBindsToThePlaceItChecked(t *testing.T) {
	t.Parallel()
	th := newTestHelper(t)
	sub := filepath.Join(th.outside, "sub")
	full := filepath.Join(th.outside, "full")
	for _, dir := range []string{sub, filepath.Join(full, "place", "x")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	th.beforeMount = func() {
		if err := os.Rename(sub, sub+".was"); err != nil {
			t.Error(err)
		}
		if err := os.Symlink(full, sub); err != nil {
			t.Error(err)
		}
	}
	err := th.bind(filepath.Join(th.mountPoint, "Work"), filepath.Join(sub, "place"))
	checked := filepath.Join(sub+".was", "place")
	if err != nil || len(th.mounted) != 1 || th.mounted[0][1] != checked {
		t.Fatalf("bind: %v; mounted %q, want the place checked, %s", err, th.mounted, checked)
	}
}

func TestTheHelperBindsOnlyFromAMountFolder(t *testing.T) {
	t.Parallel()
	th := newTestHelper(t)
	other := filepath.Join(th.mountIn, "other")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	th.ours[other] = true
	if err := th.bind(other, filepath.Join(th.outside, "place")); err == nil || len(th.mounted) != 0 {
		t.Fatalf("a folder that is no mount folder was bound: %v, %q", err, th.mounted)
	}
}
