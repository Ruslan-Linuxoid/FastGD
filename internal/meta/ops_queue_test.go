package meta

import (
	"testing"
	"time"
)

func TestChangeDuringSendingQueuesAnOperationAfterIt(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	setRoot(t, s)
	now := time.Unix(100, 0)
	it := inDrive(t, s, "f1", "a.txt")
	if err := s.MarkDirty(it.Ino, 0, false); err != nil {
		t.Fatal(err)
	}
	sending := requireWriteQueuedWithContent(t, s, it, now)
	requireRenameDuringUploadQueuesAfterIt(t, s, it, sending, now)
	requireKillRequeuesTheChangeOnce(t, s, it, sending, now)
}

func requireWriteQueuedWithContent(t *testing.T, s *Store, it Item, now time.Time) Op {
	t.Helper()
	sending, ok := ready(t, s, now)[it.Ino]

	if written, err := s.ByIno(it.Ino); err != nil || !ok || !written.Dirty || !written.RExists {
		t.Fatalf("a first write queued %+v for the item %+v: %v", sending, written, err)
	}
	return sending
}

func requireRenameDuringUploadQueuesAfterIt(t *testing.T, s *Store, it Item, sending Op, now time.Time) {
	t.Helper()
	if err := s.StartOp(sending.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Visible().Rename(it, "root", "b.txt", nil); err != nil {
		t.Fatal(err)
	}
	ops := opsOf(t, s, it.Ino)
	if len(ops) != 2 || ops[0].State != OpSending || ops[1].State != OpQueued {
		t.Fatalf("a rename during the upload left %+v", ops)
	}
	if got := ready(t, s, now); len(got) != 0 {
		t.Fatalf("the operation after the one being sent is ready: %+v", got)
	}
}

func requireKillRequeuesTheChangeOnce(t *testing.T, s *Store, it Item, sending Op, now time.Time) {
	t.Helper()

	if err := s.RequeueSending(); err != nil {
		t.Fatal(err)
	}
	ops := opsOf(t, s, it.Ino)
	if got, err := s.ByIno(it.Ino); err != nil || len(ops) != 1 || ops[0].State != OpQueued || got.Name != "b.txt" ||
		!got.Dirty || ops[0].ID != sending.ID {
		t.Fatalf("after a kill the item %+v has %+v, want the earlier operation for the write and the rename: %v", got, ops,
			err)
	}
	if got := ready(t, s, now); len(got) != 1 {
		t.Fatalf("after a kill ready are %+v", got)
	}
}

func TestOperationsNeverWaitInALoop(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	setRoot(t, s)
	a := made(t, s, KindDir, "root", "a")
	b := made(t, s, KindDir, a.ID, "b")
	opA, opB := opsOf(t, s, a.Ino)[0], opsOf(t, s, b.Ino)[0]
	tx, err := s.w.Begin()
	if err != nil {
		t.Fatal(err)
	}

	if err := need(tx, opA.ID, opB.ID); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if got := ready(t, s, time.Unix(100, 0)); len(got) != 1 || got[a.Ino].ID != opA.ID {
		t.Fatalf("ready are %+v, want the outer folder", got)
	}
}

func TestEveryChangeOfAnItemGetsItsOperation(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	setRoot(t, s)
	it := inDrive(t, s, "f1", "a.txt")
	other := inDrive(t, s, "f2", "other.txt")
	err := s.inTx("a change that knows nothing of operations", func(tx txn) error {
		_, err := tx.Exec(`UPDATE items SET name = 'b.txt', meta_dirty = 1 WHERE ino = ?`, it.Ino)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if ops := opsOf(t, s, it.Ino); len(ops) != 1 {
		t.Fatalf("the renamed item has the operations %+v, want one", ops)
	}

	renamed := file("f2", "root", "renamed.txt")
	mustPut(t, s, renamed)
	if ops := opsOf(t, s, other.Ino); len(ops) != 0 {
		t.Fatalf("an item that follows Drive has the operations %+v", ops)
	}
	var noted int
	if err := s.r.QueryRow(`SELECT count(*) FROM touched`).Scan(&noted); err != nil || noted != 0 {
		t.Fatalf("%d items are left noted as changed: %v", noted, err)
	}

	if _, err := s.w.Exec(`UPDATE items SET name = 'a.txt', meta_dirty = 1 WHERE ino = ?`, it.Ino); err != nil {
		t.Fatal(err)
	}
	if err := s.inTx("another change", func(txn) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if ops := opsOf(t, s, it.Ino); len(ops) != 0 {
		t.Fatalf("the item put back as Drive holds it has the operations %+v", ops)
	}
}
