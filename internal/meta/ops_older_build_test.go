package meta

import (
	"path/filepath"
	"testing"
	"time"
)

func TestOperationsOfAnOlderBuildOpen(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "meta.db")
	s, it := openWithARenamedItem(t, path)
	s = reopenAfter(t, path, s, []string{
		`ALTER TABLE ops ADD COLUMN kind INTEGER NOT NULL DEFAULT 2`,
		`ALTER TABLE ops ADD COLUMN before TEXT NOT NULL DEFAULT '{}'`,
		`ALTER TABLE ops ADD COLUMN after TEXT NOT NULL DEFAULT '{}'`,
		`ALTER TABLE ops ADD COLUMN source TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE ops DROP COLUMN refused_for`,
		`ALTER TABLE ops DROP COLUMN net`,
		`ALTER TABLE ops DROP COLUMN noted`,
		`UPDATE ops SET refused = 3, attempts = 4`,

		`DROP TRIGGER items_changed`, `DROP TRIGGER items_made`, `DROP TRIGGER items_gone`, `DROP TABLE touched`,
		`PRAGMA user_version = 18`,
	})
	ops := opsOf(t, s, it.Ino)
	if len(ops) != 1 || ops[0].Refused != 0 || ops[0].Attempts != 4 {
		t.Fatalf("after the upgrade the item has the operations %+v, want one with no refusals counted and its 4 tries", ops)
	}
	if got := ready(t, s, time.Unix(100, 0)); len(got) != 1 {
		t.Fatalf("after the upgrade ready are %+v", got)
	}
}

func TestTriesKeptWithTheItemsOpen(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "meta.db")
	s, it := openWithARenamedItem(t, path)
	later := time.Unix(1000, 0)
	s = reopenAfter(t, path, s, []string{
		`ALTER TABLE items ADD COLUMN attempts INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE items ADD COLUMN next_try INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE items ADD COLUMN last_error TEXT NOT NULL DEFAULT ''`,
		`CREATE INDEX items_pending ON items(next_try) WHERE dirty = 1 OR r_exists = 0 OR meta_dirty = 1`,
		`CREATE TABLE net_failures (ino INTEGER PRIMARY KEY)`,
		`INSERT INTO net_failures(ino) SELECT ino FROM ops`,
		`ALTER TABLE ops DROP COLUMN net`,
		`ALTER TABLE ops DROP COLUMN noted`,
		`UPDATE ops SET attempts = 2, last_error = 'no network', next_try = 1000000000000`,
		`DROP TRIGGER items_changed`, `DROP TRIGGER items_made`, `DROP TRIGGER items_gone`, `DROP TABLE touched`,
		`PRAGMA user_version = 19`,
	})
	if ops := opsOf(t, s, it.Ino); len(ops) != 1 || ops[0].Attempts != 2 || ops[0].LastError != "no network" ||
		!ops[0].NextTry.Equal(later) {
		t.Fatalf("after the upgrade the item has the operations %+v", ops)
	}
	if got := ready(t, s, time.Unix(100, 0)); len(got) != 0 {
		t.Fatalf("an operation waiting out its backoff is ready: %+v", got)
	}
	if err := s.RetryNetFailures(); err != nil {
		t.Fatal(err)
	}
	if got := ready(t, s, time.Unix(100, 0)); len(got) != 1 {
		t.Fatalf("after the network came back ready are %+v, want the one that failed on it", got)
	}
}

func TestJournalOfMovesOfAnOlderBuildOpens(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "meta.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	setRoot(t, s)
	s = reopenAfter(t, path, s, []string{
		`INSERT INTO state(key, value) VALUES ('shelved:took1', 'had1'), ('shelved:took2', 'had2')`,
		`DROP TABLE shelf`,
		`PRAGMA user_version = 21`,
	})
	left, err := s.Shelf().Unfinished()
	if err != nil || len(left) != 2 || left["took1"] != "had1" || left["took2"] != "had2" {
		t.Fatalf("after the upgrade the journal holds %v: %v", left, err)
	}
	if _, ok, err := s.GetState("shelved:took1"); err != nil || ok {
		t.Fatalf("the state still holds a move of the journal: %v %v", ok, err)
	}
	requireJournalFollowsItsMoves(t, s)
}

func requireJournalFollowsItsMoves(t *testing.T, s *Store) {
	t.Helper()
	if err := s.Shelf().Finished("took1"); err != nil {
		t.Fatal(err)
	}
	if left, _ := s.Shelf().Unfinished(); len(left) != 1 || left["took2"] != "had2" {
		t.Fatalf("with one move finished the journal holds %v", left)
	}

	if err := s.DeleteState("shelved:took2"); err != nil {
		t.Fatal(err)
	}
	if left, _ := s.Shelf().Unfinished(); len(left) != 1 {
		t.Fatalf("a deletion of a state key took a move out of the journal: %v", left)
	}
}

func openWithARenamedItem(t *testing.T, path string) (*Store, Item) {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	setRoot(t, s)
	it := inDrive(t, s, "f1", "a.txt")
	if err := s.Visible().Rename(it, "root", "b.txt", nil); err != nil {
		t.Fatal(err)
	}
	return s, it
}

func reopenAfter(t *testing.T, path string, s *Store, queries []string) *Store {
	t.Helper()
	for _, q := range queries {
		if _, err := s.w.Exec(q); err != nil {
			t.Fatal(q, err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatalf("the database of the older build does not open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
