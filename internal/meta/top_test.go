package meta

import (
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func TestADatabaseRootedAtMyDriveGetsTheRootOfTheMount(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "meta.db")
	olderLayout(t, path)
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if top, err := s.ByIno(RootIno); err != nil || top.ID != TopID {
		t.Fatalf("the root of the mount is %+v, %v", top, err)
	}
	if root, err := s.ByID("root"); err != nil || root.Ino == RootIno || root.Parent != TopID {
		t.Fatalf("My Drive is %+v, %v; want it under the root of the mount", root, err)
	}
	if p, err := s.Path("f1"); err != nil || p != "My Drive/docs/a.txt" {
		t.Fatalf("the file shows at %q, %v", p, err)
	}
	if names := topNames(t, s); !slices.Equal(names, []string{LostName, MyDriveName, SharedWithMeName}) {
		t.Fatalf("the root of the mount lists %q", names)
	}
}

func topNames(t *testing.T, s *Store) []string {
	t.Helper()
	entries, err := s.Children(TopID)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Display)
	}
	return names
}

func olderLayout(t *testing.T, path string) {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	setRoot(t, s)
	mustPut(t, s, dir("d1", "root", "docs"), file("f1", "d1", "a.txt"))
	if err := s.Visible().AddLost("copy", nil, "b.txt", "text/plain", 1, time.Unix(50, 0), nil,
		time.Unix(50, 0)); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`DELETE FROM items WHERE ino = 1`,
		`DELETE FROM items WHERE id LIKE ':%'`,
		`UPDATE items SET ino = 1, name = '', dname = '', shown_as = '', parent = '', r_name = '', r_parent = ''
			WHERE id = 'root'`,
		`UPDATE items SET parent = 'root' WHERE parent = ':top'`,
		`PRAGMA user_version = 25`,
	} {
		if _, err := s.w.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestADatabaseWithTheRootAndNoItemsOpens(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "meta.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetState(StateRootID, "root"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.Exec(`PRAGMA user_version = 25`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s, err = Open(path); err != nil {
		t.Fatalf("the database with no items does not open: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}
