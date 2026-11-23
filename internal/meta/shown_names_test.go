package meta

import (
	"errors"
	"maps"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func shownIn(t *testing.T, s *Store, parent string) map[string]string {
	t.Helper()
	kids, err := s.Children(parent)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, k := range kids {
		if _, twice := out[k.Display]; twice || k.Display == "" {
			t.Fatalf("the folder shows the name %q twice, or an empty one", k.Display)
		}
		out[k.Display] = k.ID
		requireNameAgreesWithTheListing(t, s, parent, k)
	}
	return out
}

func requireNameAgreesWithTheListing(t *testing.T, s *Store, parent string, k Entry) {
	t.Helper()
	if it, err := s.Lookup(parent, k.Display); err != nil || it.ID != k.ID {
		t.Fatalf("Lookup(%q) = %s, %v; the listing shows %s under it", k.Display, it.ID, err, k.ID)
	}
	if p, err := s.Path(k.ID); err != nil || filepath.Base(p) != k.Display {
		t.Fatalf("Path(%s) = %q, %v; the listing shows it as %q", k.ID, p, err, k.Display)
	}
	if name, err := s.ShownNameOf(k.Item); err != nil || name != k.Display {
		t.Fatalf("ShownNameOf(%s) = %q, %v; the listing shows it as %q", k.ID, name, err, k.Display)
	}
}

func wantShown(t *testing.T, s *Store, parent string, want map[string]string) {
	t.Helper()
	if got := shownIn(t, s, parent); !maps.Equal(got, want) {
		t.Fatalf("the folder shows %v, want %v", got, want)
	}
}

func at(sec int64, id, name string) Item {
	it := file(id, "root", name)
	it.Ctime = time.Unix(sec, 0)
	return it
}

func apply(t *testing.T, s *Store, changes ...Change) {
	t.Helper()
	if _, err := s.Visible().ApplyChanges(changes, "token"); err != nil {
		t.Fatal(err)
	}
}

func TestNumberedNames(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		dname string
		n     int
		want  string
	}{
		{"IMG.jpg", 1, "IMG (1).jpg"},
		{"IMG.jpg", 12, "IMG (12).jpg"},
		{"a.tar.gz", 2, "a.tar (2).gz"},
		{".bashrc", 1, ".bashrc (1)"},
		{"noext", 1, "noext (1)"},
		{"name.", 1, "name. (1)"},
		{".", 1, ". (1)"},
		{"", 1, "(1)"},
	} {
		if got := numbered(c.dname, c.n); got != c.want {
			t.Errorf("numbered(%q, %d) = %q, want %q", c.dname, c.n, got, c.want)
		}
	}
}

func TestDuplicatesShowUnderNumbersThatStay(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "meta.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	setRoot(t, s)

	mustPut(t, s, at(20, "b", "x.txt"), at(10, "a", "x.txt"), at(5, "dot", "."))
	wantShown(t, s, "root", map[string]string{"x.txt": "a", "x (1).txt": "b", ". (1)": "dot"})
	for _, name := range []string{".", "x (2).txt", "x [b].txt", ""} {
		if it, err := s.Lookup("root", name); !errors.Is(err, ErrNotFound) {
			t.Errorf("Lookup(%q) = %s, %v; want ErrNotFound", name, it.ID, err)
		}
	}

	mustPut(t, s, at(1, "c", "x.txt"))
	wantShown(t, s, "root", map[string]string{"x.txt": "a", "x (1).txt": "b", "x (2).txt": "c", ". (1)": "dot"})

	apply(t, s, Change{ID: "b", Removed: true})
	left := map[string]string{"x.txt": "a", "x (2).txt": "c", ". (1)": "dot"}
	wantShown(t, s, "root", left)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s, err = Open(path); err != nil {
		t.Fatal(err)
	}
	wantShown(t, s, "root", left)

	mustPut(t, s, at(30, "d", "x.txt"))
	wantShown(t, s, "root", map[string]string{"x.txt": "a", "x (1).txt": "d", "x (2).txt": "c", ". (1)": "dot"})
}

func TestARealNumberedNameHoldsItsNumber(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	setRoot(t, s)

	mustPut(t, s, at(10, "first", "a.txt"), at(20, "dup", "a.txt"), at(30, "real", "a (1).txt"))
	wantShown(t, s, "root", map[string]string{"a.txt": "first", "a (1).txt": "real", "a (2).txt": "dup"})

	mustPut(t, s, at(40, "late", "a (2).txt"))
	wantShown(t, s, "root", map[string]string{
		"a.txt": "first", "a (1).txt": "real", "a (2).txt": "dup", "a (2) (1).txt": "late",
	})
}

func TestOnlyTheChangedItemIsNamedAnew(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	setRoot(t, s)
	mustPut(t, s, dir("sub", "root", "sub"), at(10, "a", "x.txt"), at(20, "b", "x.txt"), at(30, "c", "x.txt"))
	base := map[string]string{"sub": "sub"}
	want := func(names map[string]string) {
		t.Helper()
		all := maps.Clone(base)
		maps.Copy(all, names)
		wantShown(t, s, "root", all)
	}
	want(map[string]string{"x.txt": "a", "x (1).txt": "b", "x (2).txt": "c"})

	apply(t, s, Change{ID: "a", Item: at(10, "a", "y.txt")})
	want(map[string]string{"y.txt": "a", "x (1).txt": "b", "x (2).txt": "c"})

	grown := at(20, "b", "x.txt")
	grown.Size = 7
	apply(t, s, Change{ID: "b", Item: grown})
	want(map[string]string{"y.txt": "a", "x (1).txt": "b", "x (2).txt": "c"})

	apply(t, s, Change{ID: "a", Item: at(10, "a", "x.txt")})
	want(map[string]string{"x.txt": "a", "x (1).txt": "b", "x (2).txt": "c"})

	gone := at(10, "a", "x.txt")
	gone.Trashed = true
	apply(t, s, Change{ID: "a", Item: gone})
	want(map[string]string{"x (1).txt": "b", "x (2).txt": "c"})
	apply(t, s, Change{ID: "d", Item: at(40, "d", "x.txt")})
	apply(t, s, Change{ID: "a", Item: at(10, "a", "x.txt")})
	want(map[string]string{"x.txt": "d", "x (1).txt": "b", "x (2).txt": "c", "x (3).txt": "a"})

	moved := at(30, "c", "x.txt")
	moved.Parent = "sub"
	apply(t, s, Change{ID: "c", Item: moved})
	want(map[string]string{"x.txt": "d", "x (1).txt": "b", "x (3).txt": "a"})
	wantShown(t, s, "sub", map[string]string{"x.txt": "c"})
}

func TestLongNamesAndTheirNumbersStayWithinTheLimit(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	setRoot(t, s)
	long := strings.Repeat("ё", 2000) + ".pdf"
	edge := strings.Repeat("a", MaxName-4) + ".txt"
	odd := "o." + strings.Repeat("x", 2000)
	mustPut(t, s,
		at(1, "l1", long), at(2, "l2", strings.Repeat("ё", 1999)+"я.pdf"), at(3, "l3", long),
		at(4, "e1", edge), at(5, "e2", edge), at(6, "e3", "a"+edge),
		at(7, "o1", odd), at(8, "o2", odd))
	got := shownIn(t, s, "root")
	if len(got) != 8 {
		t.Fatalf("eight items show under %d names", len(got))
	}
	by := map[string]string{}
	for name, id := range got {
		if len(name) > MaxName || !utf8.ValidString(name) {
			t.Fatalf("%s shows under %d bytes, valid %v", id, len(name), utf8.ValidString(name))
		}
		by[id] = name
	}
	requireLongNamesCut(t, by)
	requireNumbersFitTheLimit(t, by)
}

func requireLongNamesCut(t *testing.T, by map[string]string) {
	t.Helper()
	cut := strings.Repeat("ё", (MaxName-len(".pdf"))/2) + ".pdf"
	if by["l1"] != cut {
		t.Fatalf("the long name shows as %d bytes ending %q, want it cut at the end of its stem", len(by["l1"]),
			by["l1"][len(by["l1"])-12:])
	}

	for _, id := range []string{"l2", "l3"} {
		if !strings.HasSuffix(by[id], ").pdf") || !strings.HasPrefix(by[id], "ёёё") {
			t.Fatalf("%s shows as ...%q, want a numbered name", id, by[id][len(by[id])-12:])
		}
	}
}

func requireNumbersFitTheLimit(t *testing.T, by map[string]string) {
	t.Helper()
	edge := strings.Repeat("a", MaxName-4) + ".txt"
	if by["e1"] != edge {
		t.Fatalf("a name of exactly %d bytes shows as %d bytes", MaxName, len(by["e1"]))
	}
	if !strings.HasSuffix(by["e2"], "a (1).txt") {
		t.Fatalf("the duplicate of a name at the limit shows as ...%q", by["e2"][len(by["e2"])-12:])
	}
	if !strings.HasSuffix(by["e3"], "a.txt") && !strings.HasSuffix(by["e3"], ").txt") {
		t.Fatalf("a name one byte past the limit shows as ...%q", by["e3"][len(by["e3"])-12:])
	}

	if !strings.HasSuffix(by["o2"], "x (1)") {
		t.Fatalf("the duplicate of a name with a long extension shows as ...%q", by["o2"][len(by["o2"])-12:])
	}
}
