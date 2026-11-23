package binds

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTheHelperBindsInsideNoMountFolder(t *testing.T) {
	t.Parallel()
	th := newTestHelper(t)
	other := filepath.Join(th.mountIn, "FastGD - other@example.com")
	if err := os.Mkdir(other, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := th.bind(filepath.Join(th.mountPoint, "Work"), filepath.Join(other, "place")); err == nil ||
		len(th.mounted) != 0 {
		t.Fatalf("a place inside another mount folder was bound: %v, %q", err, th.mounted)
	}
}

func TestTheHelperBindsUnderAMountInThatIsALink(t *testing.T) {
	t.Parallel()
	th := newTestHelper(t)
	link := filepath.Join(th.outside, "drive")
	if err := os.Symlink(th.mountIn, link); err != nil {
		t.Fatal(err)
	}
	th.mountIn = link
	source := filepath.Join(link, filepath.Base(th.mountPoint), "Work")
	place := filepath.Join(th.outside, "place")
	if err := th.bind(source, place); err != nil || len(th.mounted) != 1 || th.mounted[0][1] != place {
		t.Fatalf("bind under a link of mount_in: %v, mounted %q", err, th.mounted)
	}
}
