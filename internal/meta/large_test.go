package meta

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func planOf(t *testing.T, s *Store, query string, args ...any) string {
	t.Helper()
	rows, err := s.r.Query(`EXPLAIN QUERY PLAN `+query, args...)
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
	return plan.String()
}

func TestQueriesOfALargeDatabaseReadTheirIndexes(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	if plan := planOf(t, s, dirtyIDs); !strings.Contains(plan, "USING COVERING INDEX items_dirty") {
		t.Errorf("the dirty files are found by\n%s", plan)
	}
	if KindDir != 1 {
		t.Errorf("a folder is of the kind %d, and heightOf and the index items_dirs name the kind 1", KindDir)
	}
	if plan := planOf(t, s, heightOf, "d", 10); !strings.Contains(plan, "INDEX items_dirs") {
		t.Errorf("the folders below a folder are found by\n%s", plan)
	}
}

func TestDirtyIDsAndHeight(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	setRoot(t, s)
	mustPut(t, s, dir("a", "root", "a"), dir("b", "a", "b"), dir("c", "b", "c"), Item{ID: "f", Name: "f", Parent: "c"},
		Item{ID: "clean", Name: "clean", Parent: "root"})
	changed := made(t, s, KindFile, "root", "changed")
	if err := s.MarkDirty(changed.Ino, 0, false); err != nil {
		t.Fatal(err)
	}
	ids, err := s.DirtyIDs()
	if err != nil || !slices.Equal(ids, []string{changed.ID}) {
		t.Fatalf("the dirty files are %v, %v; want the changed one alone", ids, err)
	}
	for folder, want := range map[string]int{"a": 2, "b": 1, "c": 0} {
		if h, err := s.Height(folder, 100); err != nil || h != want {
			t.Errorf("the folder %s holds %d levels of folders, %v; want %d", folder, h, err, want)
		}
	}
}

func TestChildCountStopsAtTheLimit(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	setRoot(t, s)
	items := make([]Item, 0, 6)
	items = append(items, dir("d", "root", "d"))
	for i := range 5 {
		items = append(items, Item{ID: fmt.Sprint("f", i), Name: fmt.Sprint("f", i), Parent: "d"})
	}
	mustPut(t, s, items...)
	for limit, want := range map[int]int{3: 3, 5: 5, 10: 5} {
		if n, err := s.ChildCount("d", limit); err != nil || n != want {
			t.Errorf("counted to %d: %d items, %v; want %d", limit, n, err, want)
		}
	}
}

func TestReplacePiecesWritesOnlyWhatDiffers(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	seedPiecesOfTwoFiles(t, s)
	written := totalChanges(t, s)

	want := []Piece{{0, 20}, {40, 10}, {60, 5}}
	if err := s.ReplacePieces(7, want); err != nil {
		t.Fatal(err)
	}
	if got, err := s.Pieces(7); err != nil || !slices.Equal(got, want) {
		t.Fatalf("the pieces are %v, %v; want %v", got, err, want)
	}
	if err := s.ReplacePieces(7, []Piece{{0, 20}, {0, 20}}); err == nil {
		t.Fatal("a piece that comes twice is taken")
	}
	if got, err := s.Pieces(8); err != nil || !slices.Equal(got, []Piece{{0, 10}}) {
		t.Fatalf("the pieces of another file are %v, %v", got, err)
	}

	if after := totalChanges(t, s); after-written != 5 {
		t.Fatalf("the replacement wrote %d records, want 5", after-written)
	}
}

func seedPiecesOfTwoFiles(t *testing.T, s *Store) {
	t.Helper()
	for _, p := range []Piece{{0, 10}, {10, 10}, {40, 10}, {90, 10}} {
		if err := s.AddPiece(7, p.Off, p.Len); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddPiece(8, 0, 10); err != nil {
		t.Fatal(err)
	}
}

func totalChanges(t *testing.T, s *Store) int {
	t.Helper()
	var n int
	if err := s.w.QueryRow(`SELECT total_changes()`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func sessionOfItem(t *testing.T, s *Store, it Item) string {
	t.Helper()
	ses, err := s.UploadSession(it.Ino)
	if err != nil {
		t.Fatal(err)
	}
	return ses.Address
}

func itemWithASession(t *testing.T, s *Store, name string) Item {
	t.Helper()
	it := made(t, s, KindFile, "root", name)
	if err := s.MarkDirty(it.Ino, 0, false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetUploadSession(it.Ino, Session{Address: "https://upload/" + name}); err != nil {
		t.Fatal(err)
	}
	if sessionOfItem(t, s, it) == "" {
		t.Fatal("scenario not built: no session")
	}
	return it
}

func TestUploadSessionGoesWithTheChangeOfItsFile(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	setRoot(t, s)
	cleaned, removed, kept := itemWithASession(t, s, "cleaned"), itemWithASession(t, s, "removed"),
		itemWithASession(t, s, "kept")
	requireSessionsGoWithTheirFiles(t, s, cleaned, removed, kept)
	requireStraySessionGoesAtTheOpen(t, s, kept)
}

func requireSessionsGoWithTheirFiles(t *testing.T, s *Store, cleaned, removed, kept Item) {
	t.Helper()
	if _, err := s.w.Exec(`UPDATE items SET dirty = 0 WHERE ino = ?`, cleaned.Ino); err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.Exec(`DELETE FROM items WHERE ino = ?`, removed.Ino); err != nil {
		t.Fatal(err)
	}
	if sessionOfItem(t, s, cleaned) != "" || sessionOfItem(t, s, removed) != "" || sessionOfItem(t, s, kept) == "" {
		t.Fatalf("sessions: of a cleaned file %q, of a removed one %q, of one still changed %q",
			sessionOfItem(t, s, cleaned), sessionOfItem(t, s, removed), sessionOfItem(t, s, kept))
	}

	if err := s.SetUploadSession(cleaned.Ino, Session{Address: "https://upload/late"}); err != nil {
		t.Fatal(err)
	}
	if sessionOfItem(t, s, cleaned) != "" {
		t.Fatal("a session is recorded for a file no longer changed here")
	}
}

func requireStraySessionGoesAtTheOpen(t *testing.T, s *Store, kept Item) {
	t.Helper()
	if _, err := s.w.Exec(`INSERT INTO upload_sessions(ino, address, name, parent, mtime) VALUES (999, ` +
		`'https://upload/stray', '', '', 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.w.Exec(schema); err != nil {
		t.Fatal(err)
	}
	if ses, err := s.UploadSession(999); err != nil || ses.Address != "" || sessionOfItem(t, s, kept) == "" {
		t.Fatalf("after the schema runs, a stray session is %q and the kept one %q", ses.Address, sessionOfItem(t, s, kept))
	}
}
