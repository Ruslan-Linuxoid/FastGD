package meta

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func openTest(tb testing.TB) *Store {
	tb.Helper()
	s, err := Open(filepath.Join(tb.TempDir(), "meta.db"))
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { s.Close() })
	return s
}

func mustPut(tb testing.TB, s *Store, items ...Item) {
	tb.Helper()
	if err := s.PutItems(items); err != nil {
		tb.Fatal(err)
	}
}

func setRoot(tb testing.TB, s *Store) {
	tb.Helper()
	if err := s.SetState(StateRootID, "root"); err != nil {
		tb.Fatal(err)
	}
	if err := s.SetRoot("root", time.Unix(0, 0)); err != nil {
		tb.Fatal(err)
	}
}

func dir(id, parent, name string) Item {
	return Item{ID: id, Kind: KindDir, Mime: mimeFolder, Name: name, Parent: parent}
}

func file(id, parent, name string) Item {
	return Item{ID: id, Kind: KindFile, Mime: "text/plain", Name: name, Parent: parent}
}

func TestByIDPathAndChildren(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	setRoot(t, s)

	mustPut(t, s, file("f1", "d2", "deep.txt"))
	mustPut(t, s, dir("d1", "root", "a"), dir("d2", "d1", "b"))

	it, err := s.ByID("f1")
	if err != nil || it.Name != "deep.txt" || it.Parent != "d2" {
		t.Fatalf("ByID = %+v, %v", it, err)
	}
	requirePathsOfTheDeepFile(t, s)
	requireTheDeepFileIsTheOnlyChild(t, s)
	root, err := s.ByIno(RootIno)
	if err != nil || root.ID != "root" {
		t.Fatalf("ByIno(root) = %+v, %v", root, err)
	}
	if _, err := s.ByID("nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("ByID(nope) err %v", err)
	}
}

func requirePathsOfTheDeepFile(t *testing.T, s *Store) {
	t.Helper()
	if p, err := s.Path("f1"); err != nil || p != "a/b/deep.txt" {
		t.Fatalf("Path = %q, %v", p, err)
	}
	if p, err := s.Path("root"); err != nil || p != "" {
		t.Fatalf("Path(root) = %q, %v", p, err)
	}
}

func requireTheDeepFileIsTheOnlyChild(t *testing.T, s *Store) {
	t.Helper()
	kids, err := s.Children("d2")
	if err != nil || len(kids) != 1 || kids[0].ID != "f1" || kids[0].Display != "deep.txt" {
		t.Fatalf("Children = %+v, %v", kids, err)
	}
}

func TestInoSurvivesUpdate(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	setRoot(t, s)
	mustPut(t, s, file("f", "root", "x"))
	before, _ := s.ByID("f")
	mustPut(t, s, file("g", "root", "y"))
	renamed := file("f", "root", "z")
	mustPut(t, s, renamed)
	after, _ := s.ByID("f")
	if before.Ino != after.Ino || after.Name != "z" {
		t.Fatalf("before %+v after %+v", before, after)
	}
}

func TestPathOfHiddenItems(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	setRoot(t, s)
	trashedDir := dir("td", "root", "trash")
	trashedDir.Trashed = true
	mustPut(t, s, trashedDir, file("in", "td", "f"), file("orphan", "", "o"), file("foreign", "elsewhere", "x"))
	for _, id := range []string{"td", "in", "orphan", "foreign"} {
		if p, err := s.Path(id); !errors.Is(err, ErrNotShown) {
			t.Errorf("Path(%s) = %q, %v; want ErrNotShown", id, p, err)
		}
	}
}

func TestPathNeedsNoEscaping(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "a?b#c%d e")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(dir, "meta.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.SetState("k", "v"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "meta.db")); err != nil {
		t.Fatal(err)
	}
}

func TestSchemaVersionMismatchRefused(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "meta.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.Exec(`PRAGMA user_version = 999`); err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := Open(path); err == nil {
		t.Fatal("opened a database with a foreign schema version")
	}
}

func BenchmarkChildren(b *testing.B) {
	s := openTest(b)
	setRoot(b, s)
	const folders, perFolder = 2000, 100
	batch := make([]Item, 0, perFolder+1)
	for d := range folders {
		did := fmt.Sprintf("d%05d", d)
		batch = append(batch[:0], dir(did, "root", did))
		for f := range perFolder {
			it := file(fmt.Sprintf("f%05d_%03d", d, f), did, fmt.Sprintf("file %03d.jpg", f))
			it.Size = int64(f)
			batch = append(batch, it)
		}
		mustPut(b, s, batch...)
	}
	n := 0
	for b.Loop() {
		kids, err := s.Children(fmt.Sprintf("d%05d", n%folders))
		if err != nil || len(kids) != perFolder {
			b.Fatalf("Children: %d, %v", len(kids), err)
		}
		n++
	}
	if per := b.Elapsed() / time.Duration(n); per >= time.Millisecond {
		b.Fatalf("listing 100 of 200000 items takes %v, want under 1ms", per)
	}
}

func TestUsageCountsByTheMinuteAndForgetsOldMinutes(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	t0 := time.Date(2026, 11, 19, 10, 0, 0, 0, time.UTC)
	addUsagesOfThreeMinutes(t, s, t0)
	if units, bytes, err := s.UsageSince(t0); err != nil || units != 305 || bytes != 4196 {
		t.Fatalf("since the start: %d units and %d bytes, %v", units, bytes, err)
	}
	if units, bytes, err := s.UsageSince(t0.Add(time.Minute)); err != nil || units != 100 || bytes != 0 {
		t.Fatalf("since a minute later: %d units and %d bytes, %v", units, bytes, err)
	}

	later := t0.Add(24*time.Hour + time.Minute)
	if err := s.AddUsage(later, 5, 0, later.Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if units, _, err := s.UsageSince(t0.Add(-time.Hour)); err != nil || units != 105 {
		t.Fatalf("after the day moved on: %d units, %v", units, err)
	}
}

func addUsagesOfThreeMinutes(t *testing.T, s *Store, t0 time.Time) {
	t.Helper()
	for _, u := range []struct {
		at           time.Time
		units, bytes int64
	}{{t0, 5, 100}, {t0.Add(30 * time.Second), 200, 4096}, {t0.Add(2 * time.Minute), 100, 0}} {
		if err := s.AddUsage(u.at, u.units, u.bytes, u.at.Add(-24*time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
}
