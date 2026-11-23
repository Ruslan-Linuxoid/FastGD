//go:build e2e

package e2e

import (
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"
)

func TestLongNamesThroughTheKernel(t *testing.T) {
	e := startEmu(t)
	cyrillic := strings.Repeat("я", 300) + ".txt"
	e.WriteFile(e.Root(), cyrillic, []byte("cyrillic"))
	e.WriteFile(e.Root(), strings.Repeat("ю", 1500)+".txt", []byte("shortened"))
	d := newDaemon(t, e)
	d.start()

	var st syscall.Statfs_t
	if err := syscall.Statfs(d.mnt, &st); err != nil || st.Namelen != 1024 {
		t.Fatalf("statfs gives a name limit of %d: %v", st.Namelen, err)
	}
	names := dirNames(t, d.mnt)
	if !slices.Contains(names, cyrillic) {
		t.Fatalf("the root does not list the %d byte name", len(cyrillic))
	}
	if got, err := os.ReadFile(d.path(cyrillic)); err != nil || string(got) != "cyrillic" {
		t.Fatalf("the %d byte name reads %q: %v", len(cyrillic), got, err)
	}
	requireShortenedNameReads(t, d, names, cyrillic)
}

func requireShortenedNameReads(t *testing.T, d *daemon, names []string, cyrillic string) {
	t.Helper()
	var short string
	for _, n := range names {
		if n != cyrillic {
			short = n
		}
	}
	if len(short) > 1024 || !strings.HasSuffix(short, "ю.txt") {
		t.Fatalf("the shortened name is %d bytes", len(short))
	}
	if got, err := os.ReadFile(d.path(short)); err != nil || string(got) != "shortened" {
		t.Fatalf("the shortened name reads %q: %v", got, err)
	}
}
