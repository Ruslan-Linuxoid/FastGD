package meta

import (
	"fmt"
	"maps"
	"strings"
	"testing"
	"time"
)

func shortcut(id, parent, name, target string) Item {
	return Item{ID: id, Kind: KindShortcut, Mime: mimeShortcut, Name: name, Parent: parent, ShortcutTarget: target}
}

func TestShortcutBesideItsTargetUnderOneName(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	setRoot(t, s)
	target := file("t1", "root", "Report")
	target.Ctime = time.Unix(1, 0)
	link := shortcut("s1", "root", "Report", "t1")
	link.Ctime = time.Unix(2, 0)
	mustPut(t, s, target, link)
	kids, err := s.Children("root")
	if err != nil {
		t.Fatal(err)
	}
	if len(kids) != 2 || kids[0].Display != "Report" || kids[1].Display != "Report (1)" {
		t.Fatalf("the folder lists %+v, want the file and its shortcut", kids)
	}
}

const (
	shortcutGroups    = 50
	shortcutFolders   = 2500
	shortcutPerFolder = 200
	shortcutLinks     = 1000
)

func BenchmarkShortcuts(b *testing.B) {
	s := openTest(b)
	setRoot(b, s)
	seedShortcutsDatabase(b, s)
	b.Run("ShortcutsTo", func(b *testing.B) { benchShortcutsTo(b, s) })
	b.Run("Children", func(b *testing.B) { benchChildrenOfShortcuts(b, s) })
}

func seedShortcutsDatabase(tb testing.TB, s *Store) {
	tb.Helper()
	var batch []Item
	for g := range shortcutGroups {
		batch = append(batch, dir(fmt.Sprintf("g%02d", g), "root", fmt.Sprintf("group %02d", g)))
	}
	mustPut(tb, s, batch...)
	for d := range shortcutFolders {
		did := fmt.Sprintf("d%05d", d)
		batch = append(batch[:0], dir(did, fmt.Sprintf("g%02d", d%shortcutGroups), did))
		for f := range shortcutPerFolder {
			batch = append(batch, file(fmt.Sprintf("f%05d_%03d", d, f), did, fmt.Sprintf("file %03d.jpg", f)))
		}
		mustPut(tb, s, batch...)
	}

	batch = append(batch[:0], dir("links", "root", "links"))
	for l := range shortcutLinks {
		batch = append(batch, shortcut(fmt.Sprintf("s%05d", l), "links", fmt.Sprintf("link %04d", l),
			fmt.Sprintf("f%05d_%03d", l, l%shortcutPerFolder)))
	}
	mustPut(tb, s, batch...)
}

func benchShortcutsTo(b *testing.B, s *Store) {
	b.Helper()
	n := 0
	for b.Loop() {
		sc, err := s.ShortcutsTo(fmt.Sprintf("f%05d_%03d", n%shortcutLinks, n%shortcutLinks%shortcutPerFolder))
		if err != nil || len(sc) != 1 {
			b.Fatalf("ShortcutsTo: %d, %v", len(sc), err)
		}
		n++
	}
	if per := b.Elapsed() / time.Duration(n); per >= time.Millisecond {
		b.Fatalf("the shortcuts to one of 500000 items take %v, want under 1ms", per)
	}
}

func benchChildrenOfShortcuts(b *testing.B, s *Store) {
	b.Helper()
	n := 0
	for b.Loop() {
		kids, err := s.Children("links")
		if err != nil || len(kids) != shortcutLinks {
			b.Fatalf("Children: %d, %v", len(kids), err)
		}
		n++
	}
	if per := b.Elapsed() / time.Duration(n); per >= 100*time.Millisecond {
		b.Fatalf("listing 1000 shortcuts among 500000 items takes %v, want under 100ms", per)
	}
}

func TestShortcutsToReadsTheIndexOfTargets(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	rows, err := s.r.Query(`EXPLAIN QUERY PLAN `+shortcutsTo, "t1", KindShortcut)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan strings.Builder
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan.WriteString(detail + "\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.String(), "USING INDEX items_target") {
		t.Fatalf("the shortcuts to an item are found by\n%s", plan.String())
	}
}

func TestListingGivesShortcutsTheirLinks(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	setRoot(t, s)
	at := func(it Item, sec int64) Item {
		it.Ctime = time.Unix(sec, 0)
		return it
	}
	gone := file("gone", "deep", "gone.txt")
	gone.Trashed = true
	mustPut(t, s,
		dir("links", "root", "links"),
		dir("a", "root", "a"),
		dir("deep", "a", "deep"),
		file("top", "root", "top.txt"),
		file("near", "links", "near.txt"),
		at(file("one", "deep", "same.txt"), 1),
		at(file("two", "deep", "same.txt"), 2),
		gone,
		shortcut("s-top", "links", "to top", "top"),
		shortcut("s-near", "links", "to near", "near"),
		shortcut("s-dir", "links", "to deep", "deep"),
		shortcut("s-one", "links", "to one", "one"),
		shortcut("s-two", "links", "to two", "two"),
		shortcut("s-gone", "links", "to gone", "gone"),
		shortcut("s-none", "links", "to none", "nowhere"),
		shortcut("s-link", "links", "to link", "s-top"),
	)
	want := map[string]string{
		"near.txt": "",
		"to top":   "../top.txt",
		"to near":  "near.txt",
		"to deep":  "../a/deep",
		"to one":   "../a/deep/same.txt",
		"to two":   "../a/deep/same (1).txt",
	}
	kids, err := s.Children("links")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, k := range kids {
		got[k.Display] = k.Link
		if k.Kind != KindShortcut {
			continue
		}
		if link, err := s.Link(k.Item); err != nil || link != k.Link {
			t.Errorf("%s: the listing gives the link %q, asked alone it is %q, %v", k.Display, k.Link, link, err)
		}
	}
	if !maps.Equal(got, want) {
		t.Fatalf("the listing gives\n%v, want\n%v", got, want)
	}
}

func TestFoldersAboveThemselvesAreNotShown(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	setRoot(t, s)
	mustPut(t, s, dir("a", "b", "a"), dir("b", "a", "b"), file("f", "a", "f.txt"), shortcut("s", "root", "to f", "f"))
	if p, err := s.Path("f"); err == nil {
		t.Fatalf("a file under a loop of folders has the path %q", p)
	}
	if kids, err := s.Children("root"); err != nil || len(kids) != 0 {
		t.Fatalf("the root lists %+v, %v, want nothing", kids, err)
	}
}

func TestListingIsOfOneStateOfTheDatabase(t *testing.T) {
	s := openTest(t)
	setRoot(t, s)
	mustPut(t, s, dir("links", "root", "links"), file("top", "root", "top.txt"), shortcut("s-top", "links", "to top",
		"top"))
	betweenListingReads = func() {
		betweenListingReads = nil
		if _, err := s.Visible().ApplyChanges([]Change{{ID: "top", Removed: true}}, "t2"); err != nil {
			t.Error(err)
		}
	}
	defer func() { betweenListingReads = nil }()
	kids, err := s.Children("links")
	if err != nil {
		t.Fatal(err)
	}
	if len(kids) != 1 || kids[0].Link != "../top.txt" {
		t.Fatalf("the listing a removal landed in the middle of gives %+v, want the shortcut with its link", kids)
	}
	if kids, err := s.Children("links"); err != nil || len(kids) != 0 {
		t.Fatalf("the listing after the removal gives %+v, %v", kids, err)
	}
}
