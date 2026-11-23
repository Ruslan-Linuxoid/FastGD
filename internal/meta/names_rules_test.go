package meta

import (
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func staleGroups(t *testing.T, s *Store) []string {
	t.Helper()
	rows, err := s.r.Query(`SELECT ` + itemCols + ` FROM items`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		it, dname, err := scanItem(rows)
		if err != nil {
			t.Fatal(err)
		}
		if dname != it.GroupName() {
			out = append(out, it.Name+" stored as "+dname)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestEveryWriteOfANameKeepsTheNameOfTheGroup(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	setRoot(t, s)
	if err := s.AddIDs([]string{"id1", "id2", "id3", "id4", "id5", "id6"}); err != nil {
		t.Fatal(err)
	}
	doc := Item{ID: "doc", Kind: KindDoc, Mime: googlePrefix + "document", Name: "Report", Parent: "root", RName: "Report",
		RParent: "root"}
	file := Item{ID: "f1", Kind: KindFile, Mime: "text/plain", Name: "a/b.txt", Parent: "root", RName: "a/b.txt",
		RParent: "root"}
	mustPut(t, s, doc, file)
	requireGroupsKept(t, s, "a load from Drive")
	v := s.Visible()
	requireGroupsKeptByRenames(t, s, v)
	requireGroupsKeptByCreatesAndMoves(t, s, v)
}

func requireGroupsKept(t *testing.T, s *Store, after string) {
	t.Helper()
	if stale := staleGroups(t, s); len(stale) != 0 {
		t.Fatalf("after %s: %q", after, stale)
	}
}

func itemOfID(t *testing.T, s *Store, id string) Item {
	t.Helper()
	it, err := s.ByID(id)
	if err != nil {
		t.Fatal(err)
	}
	return it
}

func requireGroupsKeptByRenames(t *testing.T, s *Store, v Visible) {
	t.Helper()
	if err := v.Rename(itemOfID(t, s, "doc"), "root", "Plan", nil); err != nil {
		t.Fatal(err)
	}
	requireGroupsKept(t, s, "a rename")
	if err := v.TakeRemoteName(itemOfID(t, s, "doc"), "Theirs", "root"); err != nil {
		t.Fatal(err)
	}
	requireGroupsKept(t, s, "taking Drive's name")
	if err := v.Rename(itemOfID(t, s, "doc"), "root", "Mine", nil); err != nil {
		t.Fatal(err)
	}
	if err := v.RevertMeta(itemOfID(t, s, "doc")); err != nil {
		t.Fatal(err)
	}
	requireGroupsKept(t, s, "the undoing of a rename")
}

func requireGroupsKeptByCreatesAndMoves(t *testing.T, s *Store, v Visible) {
	t.Helper()
	made, err := v.Create(Item{Kind: KindFile, Name: "new: file.txt", Parent: "root"})
	if err != nil {
		t.Fatal(err)
	}
	requireGroupsKept(t, s, "a create")
	file := itemOfID(t, s, "f1")
	other, err := v.Create(Item{Kind: KindFile, Name: "tmp", Parent: "root"})
	if err != nil {
		t.Fatal(err)
	}
	if err := v.Replace(other, file.Ino, "root", "a/b.txt", nil); err != nil {
		t.Fatal(err)
	}
	requireGroupsKept(t, s, "a rename over another item")
	if _, err := v.Shelve(made, "", []string{"dir: one"}, "new (conflict).txt", nil, nil, time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	requireGroupsKept(t, s, "a move into Lost and Found")
}

func TestNamesOfGroupsFollowTheRulesOfTheBuild(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "meta.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	setRoot(t, s)
	mustPut(t, s, Item{ID: "doc", Kind: KindDoc, Mime: googlePrefix + "document", Name: "Report", Parent: "root"})
	spoil := func() {
		t.Helper()
		if _, err := s.w.Exec(`UPDATE items SET dname = 'of other rules' WHERE id = 'doc'`); err != nil {
			t.Fatal(err)
		}
	}
	reopen := func() {
		t.Helper()
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if s, err = Open(path); err != nil {
			t.Fatal(err)
		}
	}

	spoil()
	reopen()
	if stale := staleGroups(t, s); len(stale) != 1 {
		t.Fatalf("a database of this build's rules was gone over: %q", stale)
	}

	if err := s.SetState(stateNameRules, "1"); err != nil {
		t.Fatal(err)
	}
	reopen()
	defer s.Close()
	if stale := staleGroups(t, s); len(stale) != 0 {
		t.Fatalf("after the opening of a database of older rules: %q", stale)
	}
	if rules, _, err := s.GetState(stateNameRules); err != nil || rules != strconv.Itoa(nameRules) {
		t.Fatalf("the database tells the rules %q: %v", rules, err)
	}
}
