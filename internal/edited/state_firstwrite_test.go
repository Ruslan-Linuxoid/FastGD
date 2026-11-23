package edited

import (
	"errors"
	"slices"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func TestFirstWriteFetchesWhatItKeeps(t *testing.T) {
	size := 5 * page()
	for _, c := range []struct {
		name     string
		off, end int64
		want     []span
	}{
		{"inside a page", page() + 100, page() + 200, []span{{0, size}}},
		{"whole pages in the middle", page(), 3 * page(), []span{{0, page()}, {3 * page(), size}}},
		{"from inside a page to inside another", page() + 1, 4*page() - 1, []span{{0, 2 * page()}, {3 * page(), size}}},
		{"the start", 0, 2 * page(), []span{{2 * page(), size}}},
		{"up to the end", 2*page() + 5, size, []span{{0, 3 * page()}}},
		{"the whole file", 0, size, nil},
		{"the whole file and more", 0, size + 10, nil},
		{"past the end", size + page(), size + 2*page(), []span{{0, size}}},
		{"right after an end inside a page", size - 100, size, []span{{0, size}}},
		{"no bytes", page(), page(), []span{{0, size}}},
	} {
		if got := keptBase(c.off, c.end, size); !slices.Equal(got, c.want) {
			t.Errorf("%s: a write of [%d, %d) keeps %v, want %v", c.name, c.off, c.end, got, c.want)
		}
	}
	if got := keptBase(0, 10, 0); len(got) != 0 {
		t.Errorf("a write into an empty file keeps %v", got)
	}
}

func TestFirstWriteGoesInOnlyOverTheVersionItFetched(t *testing.T) {
	d := &inDrive{id: "id-a", content: make([]byte, 3*page())}
	k := openOver(t, t.TempDir(), d, d.stream, nil)
	it := k.fromDrive(t, d)
	same := func(int64) int64 { return it.Size }
	wrote := false
	write := func(*State, meta.Item) error { wrote = true; return nil }
	other, local := it, it
	other.MD5 = "of the version before the poll"
	local.Dirty = true
	for name, based := range map[string]meta.Item{"another version": other, "taken for one changed here": local} {
		if err := k.change(based, same, write); !errors.Is(err, errAnotherBase) {
			t.Fatalf("%s: the change ended with %v", name, err)
		}
		if now := k.item(t, it.Ino); now.Dirty || wrote {
			t.Fatalf("%s: the file is changed here %v, written %v", name, now.Dirty, wrote)
		}
	}
	if err := k.change(it, same, write); err != nil {
		t.Fatal(err)
	}
	if now := k.item(t, it.Ino); !now.Dirty || !wrote {
		t.Fatalf("over the version fetched the file is changed here %v, written %v", now.Dirty, wrote)
	}
}
