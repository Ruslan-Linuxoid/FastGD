package meta

import (
	"path/filepath"
	"testing"
	"time"
)

func TestRefusalsCountWhileTheItemAsksForTheSame(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	setRoot(t, s)
	it := inDrive(t, s, "f1", "a.txt")
	if err := s.Visible().Rename(it, "root", "b.txt", nil); err != nil {
		t.Fatal(err)
	}
	if a, b := refuseTheOp(t, s, it, true), refuseTheOp(t, s, it, true); a != 1 || b != 2 {
		t.Fatalf("two refusals in a row count %d and %d", a, b)
	}
	renamed, err := s.ByIno(it.Ino)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Visible().Rename(renamed, "root", "c.txt", nil); err != nil {
		t.Fatal(err)
	}
	if got := refuseTheOp(t, s, it, true); got != 1 {
		t.Fatalf("a refusal after another name was given counts %d, want 1", got)
	}
	requireRefusalCountStartsAnewAfterAnotherFailure(t, s, it)
}

func refuseTheOp(t *testing.T, s *Store, it Item, refused bool) int {
	t.Helper()
	op := opsOf(t, s, it.Ino)[0]
	if err := s.StartOp(op.ID); err != nil {
		t.Fatal(err)
	}
	why := Failed
	if refused {
		why = FailedRefused
	}
	if err := s.FailOp(op, op.Attempts+1, time.Unix(0, 0), "no", why); err != nil {
		t.Fatal(err)
	}
	return opsOf(t, s, it.Ino)[0].Refused
}

func requireRefusalCountStartsAnewAfterAnotherFailure(t *testing.T, s *Store, it Item) {
	t.Helper()
	if a, b := refuseTheOp(t, s, it, true), refuseTheOp(t, s, it, false); a != 2 || b != 0 {
		t.Fatalf("a refusal and a failure of another kind count %d and %d, want 2 and 0", a, b)
	}
	if got := refuseTheOp(t, s, it, true); got != 1 {
		t.Fatalf("a refusal after a failure of another kind counts %d, want 1", got)
	}
}

func TestBusyAnswersCountWhileTheItemAsksForTheSame(t *testing.T) {
	t.Parallel()
	s, it := openOfAnOlderBuildWithARename(t, filepath.Join(t.TempDir(), "meta.db"))
	if op := opsOf(t, s, it.Ino)[0]; op.Busy != 0 {
		t.Fatalf("after the upgrade the operation counts %d busy answers", op.Busy)
	}
	failTheOp(t, s, it, FailedBusy)
	failTheOp(t, s, it, FailedOffline)
	failTheOp(t, s, it, FailedRefused)
	now, err := s.ByIno(it.Ino)
	if err != nil {
		t.Fatal(err)
	}
	if op := failTheOp(t, s, it, FailedBusy); op.Busy != 2 || op.BusyWith(now) != 3 {
		t.Fatalf("two busy answers with other failures between count as %d, the next as %d", op.Busy, op.BusyWith(now))
	}
	requireBusyCountStartsAnewWithAnotherName(t, s, it, now)
}

func openOfAnOlderBuildWithARename(t *testing.T, path string) (*Store, Item) {
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
	for _, q := range []string{
		`ALTER TABLE ops DROP COLUMN busy`, `ALTER TABLE ops DROP COLUMN busy_for`, `PRAGMA user_version = 22`,
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
	t.Cleanup(func() { s.Close() })
	return s, it
}

func failTheOp(t *testing.T, s *Store, it Item, why Failure) Op {
	t.Helper()
	op := opsOf(t, s, it.Ino)[0]
	if err := s.FailOp(op, op.Attempts+1, time.Unix(0, 0), "no", why); err != nil {
		t.Fatal(err)
	}
	return opsOf(t, s, it.Ino)[0]
}

func requireBusyCountStartsAnewWithAnotherName(t *testing.T, s *Store, it, now Item) {
	t.Helper()

	if err := s.Visible().Rename(now, "root", "c.txt", nil); err != nil {
		t.Fatal(err)
	}
	now, err := s.ByIno(it.Ino)
	if err != nil {
		t.Fatal(err)
	}
	if op := opsOf(t, s, it.Ino)[0]; op.BusyWith(now) != 1 {
		t.Fatalf("with another name asked for the next busy answer counts as %d", op.BusyWith(now))
	}
	if op := failTheOp(t, s, it, FailedBusy); op.Busy != 1 {
		t.Fatalf("the first busy answer for the new name counts as %d", op.Busy)
	}
}
