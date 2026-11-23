package meta

import (
	"maps"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDatabaseWithoutShownNamesGetsThemAtAStart(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "meta.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	setRoot(t, s)
	long := strings.Repeat("b", 1100) + ".txt"
	gone := at(5, "gone", "x.txt")
	gone.Trashed = true
	mustPut(t, s, at(30, "c", "x.txt"), at(10, "a", "x.txt"), at(20, "b", "x.txt"), at(1, "real", "x (1).txt"), gone, at(2,
		"long", long))
	for _, q := range []string{
		`DROP TRIGGER items_renamed`, `DROP INDEX items_shown`, `DROP INDEX items_unnamed`,
		`ALTER TABLE items DROP COLUMN shown_as`,

		`UPDATE items SET dname = '` + strings.Repeat("b", MaxName-len(" [12345678].txt")) + `.txt' WHERE id = 'long'`,
		`UPDATE state SET value = '2' WHERE key = 'name_rules'`,
		`PRAGMA user_version = 24`,
	} {
		if _, err := s.w.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s, err = Open(path); err != nil {
		t.Fatalf("the database of the older build does not open: %v", err)
	}
	want := map[string]string{
		"x.txt": "a", "x (1).txt": "real", "x (2).txt": "b", "x (3).txt": "c",
		strings.Repeat("b", MaxName-4) + ".txt": "long",
	}
	wantShown(t, s, "root", want)

	apply(t, s, Change{ID: "a", Removed: true})
	delete(want, "x.txt")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s, err = Open(path); err != nil {
		t.Fatal(err)
	}
	wantShown(t, s, "root", want)
}

func TestHiddenShortcutGivesUpItsName(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	setRoot(t, s)
	link := shortcut("link", "root", "x", "target")
	mustPut(t, s, dir("far", "root", "far"), file("target", "far", "t"), link)
	wantShown(t, s, "root", map[string]string{"far": "far", "x": "link"})

	gone := file("target", "far", "t")
	gone.Trashed = true
	apply(t, s, Change{ID: "target", Item: gone})
	wantShown(t, s, "root", map[string]string{"far": "far"})
	apply(t, s, Change{ID: "f", Item: file("f", "root", "x")})
	wantShown(t, s, "root", map[string]string{"far": "far", "x": "f"})

	apply(t, s, Change{ID: "target", Item: file("target", "far", "t")})
	wantShown(t, s, "root", map[string]string{"far": "far", "x": "f", "x (1)": "link"})
}

func TestItemsOfAFolderKeepTheirNamesWhenItGetsItsFileID(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	setRoot(t, s)
	folder := dir("key", "root", "made here")
	later, earlier := at(20, "a", "x.txt"), at(10, "b", "x.txt")
	later.Parent, earlier.Parent = "key", "key"
	mustPut(t, s, folder, later)

	mustPut(t, s, earlier)
	want := map[string]string{"x.txt": "a", "x (1).txt": "b"}
	wantShown(t, s, "key", want)
	folder, err := s.ByID("key")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Visible().Rebind(folder, "fileid"); err != nil {
		t.Fatal(err)
	}
	wantShown(t, s, "fileid", want)
}

func TestLostAndFoundTakesItsNameAtAStart(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "meta.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	setRoot(t, s)
	theirs := dir("theirs", "root", LostName)
	theirs.Ctime = time.Unix(1, 0)
	mustPut(t, s, theirs)
	wantShown(t, s, "root", map[string]string{LostName: "theirs"})
	got := addLostAndRequireNames(t, s)

	if _, err := s.w.Exec(`UPDATE items SET shown_as = ''`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if s, err = Open(path); err != nil {
		t.Fatal(err)
	}
	if again := shownIn(t, s, "root"); !maps.Equal(again, got) {
		t.Fatalf("after the names were given anew the root shows %v, want %v", again, got)
	}
}

func TestNulInADriveNameShowsAsItsStandIn(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	setRoot(t, s)
	mustPut(t, s, at(1, "odd", "a\x00b/c.txt"))
	wantShown(t, s, "root", map[string]string{"a␀b／c.txt": "odd"})
}

func addLostAndRequireNames(t *testing.T, s *Store) map[string]string {
	t.Helper()
	if err := s.Visible().AddLost("copy", nil, "a.txt", "text/plain", 1, time.Unix(50, 0), nil, time.Unix(50,
		0)); err != nil {
		t.Fatal(err)
	}
	got := shownIn(t, s, "root")
	if got[LostName+" (1)"] != "theirs" || len(got) != 2 || got[LostName] == "theirs" || got[LostName] == "" {
		t.Fatalf("the root shows %v, want Lost and Found under its name and the folder of Drive under a number", got)
	}
	return got
}
