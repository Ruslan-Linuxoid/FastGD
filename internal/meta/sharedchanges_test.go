package meta

import (
	"errors"
	"testing"
)

func countOfShared(t *testing.T, s *Store) int64 {
	t.Helper()
	tx, err := s.r.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	n, err := sharedChanges(tx)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func sharedWithAPlan(t *testing.T) (s *Store, plan, mine, target Item) {
	t.Helper()
	s = openTest(t)
	setRoot(t, s)
	project := dir("p", SharedWithMeID, "Project")
	project.Shared = true
	plan = file("f", "p", "plan.txt")
	plan.Shared = true
	link := Item{ID: "l", Kind: KindShortcut, Mime: mimeShortcut, Name: "l", Parent: SharedWithMeID,
		ShortcutTarget: "t", Shared: true}
	mine = dir("m", "root", "Mine")
	target = file("t", "m", "t.txt")
	mustPut(t, s, project, plan, mine, target, link)
	return s, plan, mine, target
}

func TestOnlyTheRowsOfSharedWithMeChangeItsList(t *testing.T) {
	t.Parallel()
	s, plan, mine, target := sharedWithAPlan(t)
	if _, err := s.Lookup(SharedWithMeID, "plan.txt"); err != nil {
		t.Fatal(err)
	}
	step := func(what string, moves bool, items ...Item) {
		t.Helper()
		was := countOfShared(t, s)
		mustPut(t, s, items...)
		if moved := countOfShared(t, s) != was; moved != moves {
			t.Fatalf("%s moves the count: %v, want %v", what, moved, moves)
		}
	}
	written := file("w", "m", "w.txt")
	step("a file made outside the collection", false, written)
	written.Size = 10
	step("a file written outside the collection", false, written)
	mine.Name = "Mine 2"
	step("a folder renamed", false, mine)
	plan.Size, plan.Rev = 5, "r2"
	step("an item of the collection written", false, plan)
	if it, err := s.Lookup(SharedWithMeID, "plan.txt"); err != nil || it.Size != 5 {
		t.Fatalf("the second name after the write has the size %d, %v; want 5", it.Size, err)
	}
	plan.Name = "plan-r.txt"
	step("an item of the collection renamed", true, plan)
	if _, err := s.Lookup(SharedWithMeID, "plan.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the old second name after the rename: %v", err)
	}
	if _, err := s.Lookup(SharedWithMeID, "plan-r.txt"); err != nil {
		t.Fatalf("the new second name after the rename: %v", err)
	}
	target.Trashed = true
	step("the target of a shortcut of the collection trashed", true, target)
	mine.Parent = "p"
	step("a folder moved", true, mine)
	other := file("o", "m", "other.txt")
	other.Shared = true
	step("an item come into the collection", true, other)
}

func sharedNames(t *testing.T, s *Store) map[string]int64 {
	t.Helper()
	entries, err := s.Children(SharedWithMeID)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]int64{}
	for _, e := range entries {
		out[e.Display] = e.Size
	}
	return out
}

func TestTheListingOfSharedWithMeIsOfTheRowsNow(t *testing.T) {
	t.Parallel()
	s, plan, _, _ := sharedWithAPlan(t)
	if got := sharedNames(t, s); got["plan.txt"] != 0 || len(got) != 3 {
		t.Fatalf("Shared with me lists %v", got)
	}
	plan.Size = 7
	mustPut(t, s, plan)
	if got := sharedNames(t, s)["plan.txt"]; got != 7 {
		t.Fatalf("the listing shows the size %d of the written file, want 7", got)
	}
	plan.Name = "plan-r.txt"
	other := file("o", "p", "other.txt")
	other.Shared = true
	mustPut(t, s, plan, other)
	got := sharedNames(t, s)
	if _, old := got["plan.txt"]; old || got["plan-r.txt"] != 7 {
		t.Fatalf("after the rename Shared with me lists %v", got)
	}
	if _, ok := got["other.txt"]; !ok {
		t.Fatalf("the item come into the collection is not listed: %v", got)
	}
}
