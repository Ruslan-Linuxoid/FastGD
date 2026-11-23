package meta

import (
	"testing"
	"time"
)

func made(t *testing.T, s *Store, kind Kind, parent, name string) Item {
	t.Helper()
	if err := s.AddIDs([]string{"id-" + parent + "-" + name}); err != nil {
		t.Fatal(err)
	}
	mime := "text/plain"
	if kind == KindDir {
		mime = mimeFolder
	}
	it, err := s.Visible().Create(Item{Kind: kind, Mime: mime, Name: name, Parent: parent})
	if err != nil {
		t.Fatal(err)
	}
	return it
}

func ready(t *testing.T, s *Store, at time.Time) map[Ino]Op {
	t.Helper()
	ops, err := s.ReadyOps(at, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	out := map[Ino]Op{}
	for _, op := range ops {
		out[op.Ino] = op
	}
	return out
}

func opsOf(t *testing.T, s *Store, ino Ino) []Op {
	t.Helper()
	ops, err := s.Ops(ino)
	if err != nil {
		t.Fatal(err)
	}
	return ops
}

func inDrive(t *testing.T, s *Store, id, name string) Item {
	t.Helper()
	mustPut(t, s, file(id, "root", name))
	it, err := s.ByID(id)
	if err != nil {
		t.Fatal(err)
	}
	return it
}

func TestOperationWaitsForItsFolderAndNoOther(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	setRoot(t, s)
	now := time.Unix(100, 0)
	dir := made(t, s, KindDir, "root", "new")
	in := made(t, s, KindFile, dir.ID, "in.txt")
	deep := made(t, s, KindDir, dir.ID, "deep")
	apart := made(t, s, KindFile, "root", "apart.txt")
	got := ready(t, s, now)

	if _, ok := got[dir.Ino]; !ok || len(got) != 2 || got[apart.Ino].ID == 0 || apart.RExists || dir.RExists {
		t.Fatalf("ready are %+v, want the folder and the file apart from it", got)
	}
	requireFailedFolderHoldsWhatLiesInIt(t, s, got[dir.Ino], dir, apart, now)
	requireCreatedFolderLetsThemGo(t, s, dir, in, deep, now)
}

func requireFailedFolderHoldsWhatLiesInIt(t *testing.T, s *Store, op Op, dir, apart Item, now time.Time) {
	t.Helper()

	if err := s.StartOp(op.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.FailOp(op, 1, now.Add(time.Minute), "503", Failed); err != nil {
		t.Fatal(err)
	}
	if got := ready(t, s, now); len(got) != 1 || got[apart.Ino].ID == 0 {
		t.Fatalf("with the folder's creation failed, ready are %+v", got)
	}
	after, err := s.Successors(opsOf(t, s, dir.Ino)[0].ID)
	if err != nil || len(after) != 2 {
		t.Fatalf("the folder's creation has %d successors, %v, want what lies in it", len(after), err)
	}
}

func requireCreatedFolderLetsThemGo(t *testing.T, s *Store, dir, in, deep Item, now time.Time) {
	t.Helper()

	again := ready(t, s, now.Add(time.Hour))[dir.Ino]
	if again.Attempts != 1 || again.LastError != "503" {
		t.Fatalf("the failed operation comes back as %+v", again)
	}
	if err := s.MarkCreated(dir.Ino, Remote{}, Sent{Name: "new", Parent: "root"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DoneOp(again); err != nil {
		t.Fatal(err)
	}
	got := ready(t, s, now.Add(time.Hour))
	if _, ok := got[in.Ino]; !ok || got[deep.Ino].ID == 0 || deep.RExists || len(opsOf(t, s, dir.Ino)) != 0 {
		t.Fatalf("with the folder created, ready are %+v", got)
	}
}

func TestChangesOfOneItemGoIntoItsQueuedOperation(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	setRoot(t, s)
	it := inDrive(t, s, "f1", "a.txt")
	if err := s.Visible().Rename(it, "root", "b.txt", nil); err != nil {
		t.Fatal(err)
	}
	first := requireOneQueuedOp(t, s, it, "a rename")
	if err := s.Visible().Rename(it, "root", "c.txt", nil); err != nil {
		t.Fatal(err)
	}
	if op, got := requireOneQueuedOp(t, s, it, "two renames"), itemNow(t, s, it); op.ID != first.ID || got.Dirty ||
		got.RName != "a.txt" || got.Name != "c.txt" {
		t.Fatalf("two renames left %+v of the item %+v", op, got)
	}
	requireRenameAndRemovalAreOneOperation(t, s, it, first)
}

func requireOneQueuedOp(t *testing.T, s *Store, it Item, what string) Op {
	t.Helper()
	ops := opsOf(t, s, it.Ino)
	if len(ops) != 1 || ops[0].State != OpQueued {
		t.Fatalf("after %s the item has the operations %+v, want one in the queue", what, ops)
	}
	return ops[0]
}

func itemNow(t *testing.T, s *Store, it Item) Item {
	t.Helper()
	got, err := s.ByIno(it.Ino)
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func requireRenameAndRemovalAreOneOperation(t *testing.T, s *Store, it Item, first Op) {
	t.Helper()

	if err := s.Visible().Trash(it.Ino); err != nil {
		t.Fatal(err)
	}
	if op, got := requireOneQueuedOp(t, s, it, "a rename and a removal"), itemNow(t, s, it); op.ID != first.ID ||
		!got.Trashed || got.Name != "c.txt" {
		t.Fatalf("a rename and a removal left %+v of the item %+v", op, got)
	}
}

func TestChangeBackLeavesNoOperation(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	setRoot(t, s)
	it := inDrive(t, s, "f1", "a.txt")
	if err := s.Visible().Rename(it, "root", "b.txt", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Visible().Rename(it, "root", "a.txt", nil); err != nil {
		t.Fatal(err)
	}
	if ops := opsOf(t, s, it.Ino); len(ops) != 0 {
		t.Fatalf("a rename and one back left %+v", ops)
	}
	if now, _ := s.ByIno(it.Ino); now.MetaDirty {
		t.Fatal("the item put back as Drive holds it still counts as changed here")
	}
}

func TestCreationAbsorbsWhatFollowsAndATrashingUndoesIt(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	setRoot(t, s)
	it := made(t, s, KindFile, "root", "new.txt")
	when := time.Unix(500, 0)
	if err := s.SetMtime(it.Ino, when); err != nil {
		t.Fatal(err)
	}
	if err := s.Visible().Rename(it, "root", "named.txt", nil); err != nil {
		t.Fatal(err)
	}
	ops := opsOf(t, s, it.Ino)
	got, err := s.ByIno(it.Ino)
	if err != nil {
		t.Fatal(err)
	}
	if len(ops) != 1 || got.RExists || !got.Mtime.Equal(when) || got.Name != "named.txt" {
		t.Fatalf("a creation, a time and a rename left %+v of the item %+v", ops, got)
	}
	if err := s.Visible().Trash(it.Ino); err != nil {
		t.Fatal(err)
	}
	if ops := opsOf(t, s, it.Ino); len(ops) != 0 {
		t.Fatalf("a creation and a trashing left %+v", ops)
	}
}
