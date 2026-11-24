package meta

import (
	"errors"
	"testing"
	"time"
)

func TestRenameOutOfLostAndFoundLeavesItWhole(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	setRoot(t, s)
	if err := s.AddIDs([]string{"id1", "id2", "id3"}); err != nil {
		t.Fatal(err)
	}
	it, err := s.Visible().Create(Item{Kind: KindFile, Name: "a.txt", Parent: "root"})
	if err != nil {
		t.Fatal(err)
	}
	sh, err := s.Visible().Shelve(it, "", []string{"docs"}, "a (conflict).txt", nil, nil, time.Unix(100, 0))
	if err != nil {
		t.Fatal(err)
	}
	if len(sh.Dirs) != 2 || !sh.Lost.Lost {
		t.Fatalf("scenario not built: the move left %+v", sh)
	}
	root, err := s.ByID("root")
	if err != nil {
		t.Fatal(err)
	}
	requireLostAndFoundKeepsToItself(t, it, root, sh)
	v := s.Visible()
	if err := v.Rename(sh.Lost, "root", "back.txt", nil); err != nil {
		t.Fatal(err)
	}
	requireItemTakenOutOfLostAndFound(t, s, it)
	requireEmptiedFoldersGone(t, s, v, sh)
}

func requireLostAndFoundKeepsToItself(t *testing.T, it, root Item, sh Shelved) {
	t.Helper()
	if err := MayMove(sh.Lost, sh.Dirs[1], sh.Dirs[1]); err != nil {
		t.Fatalf("a rename inside Lost and Found is refused: %v", err)
	}
	if err := MayMove(it, root, sh.Dirs[1]); !errors.Is(err, ErrLostOnly) {
		t.Fatalf("a move into Lost and Found got %v", err)
	}
	if err := MayMove(sh.Dirs[0], root, root); !errors.Is(err, ErrLostOnly) {
		t.Fatalf("a rename of Lost and Found itself got %v", err)
	}
	if err := MayCreateIn(sh.Dirs[1]); !errors.Is(err, ErrLostOnly) {
		t.Fatalf("a create in Lost and Found got %v", err)
	}
}

func requireItemTakenOutOfLostAndFound(t *testing.T, s *Store, it Item) {
	t.Helper()
	now, err := s.ByIno(it.Ino)
	if err != nil || now.Lost || now.Name != "back.txt" || now.Standing() != ToCreate {
		t.Fatalf("after the rename the item is %+v: %v", now, err)
	}
	if ops := opsOf(t, s, it.Ino); len(ops) != 1 {
		t.Fatalf("the item taken out has the operations %+v", ops)
	}
}

func requireEmptiedFoldersGone(t *testing.T, s *Store, v Visible, sh Shelved) {
	t.Helper()
	for _, dir := range sh.Dirs {
		if _, err := s.ByIno(dir.Ino); !errors.Is(err, ErrNotFound) {
			t.Fatalf("the emptied folder %q of Lost and Found is still there: %v", dir.Name, err)
		}
	}
	gone := v.Pruned()
	if len(gone) != 2 || gone[0] != (Gone{sh.Dirs[0].Ino, "docs"}) || gone[1] != (Gone{RootIno, LostName}) {
		t.Fatalf("the names noted as gone are %+v", gone)
	}
}

func TestItemWithAKeyOfItsOwnTakesAFileID(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	setRoot(t, s)
	v := s.Visible()
	dir, err := v.Create(Item{Kind: KindDir, Name: "dir", Parent: "root"})
	if err != nil || !IsLocalKey(dir.ID) {
		t.Fatalf("with no fileId in store a folder was made as %q: %v", dir.ID, err)
	}
	file, err := v.Create(Item{Kind: KindFile, Name: "f.txt", Parent: dir.ID})
	if err != nil || !IsLocalKey(file.ID) || file.ID == dir.ID {
		t.Fatalf("with no fileId in store a file was made as %q: %v", file.ID, err)
	}
	file = requireFolderTakesAFileID(t, s, dir, file)
	key := file.ID
	if err := v.Rebind(file, "1file"); err != nil {
		t.Fatal(err)
	}
	if left, _ := s.Shelf().Unfinished(); len(left) != 1 || left["1file"] != key {
		t.Fatalf("the journal holds %v, want the file's fileId by its key", left)
	}
	if ops := opsOf(t, s, file.Ino); len(ops) != 1 {
		t.Fatalf("the file that took a fileId has the operations %+v", ops)
	}
}

func requireFolderTakesAFileID(t *testing.T, s *Store, dir, file Item) Item {
	t.Helper()
	if err := s.Visible().Rebind(dir, "1dir"); err != nil {
		t.Fatal(err)
	}
	now, err := s.ByIno(dir.Ino)
	if err != nil || now.ID != "1dir" {
		t.Fatalf("the folder has the fileId %q: %v", now.ID, err)
	}
	if file, err = s.ByIno(file.Ino); err != nil || file.Parent != "1dir" {
		t.Fatalf("the file in the folder names the folder %q: %v", file.Parent, err)
	}
	if p, err := s.Path(file.ID); err != nil || p != "My Drive/dir/f.txt" {
		t.Fatalf("the mount shows the file at %q: %v", p, err)
	}
	if left, _ := s.Shelf().Unfinished(); len(left) != 0 {
		t.Fatalf("a folder, which has no content, is in the journal: %v", left)
	}
	return file
}

func TestPoolGivesNoFileIDThatIsTaken(t *testing.T) {
	t.Parallel()
	s := openTest(t)
	setRoot(t, s)
	if err := s.AddIDs([]string{"id1", "id2", "id3", "id4"}); err != nil {
		t.Fatal(err)
	}
	mustPut(t, s, Item{ID: "id1", Kind: KindFile, Mime: "text/plain", Name: "a", Parent: "root", RName: "a",
		RParent: "root"},
		Item{ID: "id3", Kind: KindFile, Mime: "text/plain", Name: "b", Parent: "root", RName: "b", RParent: "root"})
	if err := s.DropIDs(func(id string) bool { return id == "id2" }); err != nil {
		t.Fatal(err)
	}
	if id, err := s.TakeID(); err != nil || id != "id4" {
		t.Fatalf("the pool gave %q: %v, want id4, the only fileId not taken", id, err)
	}
	if _, err := s.TakeID(); !errors.Is(err, ErrNoIDs) {
		t.Fatalf("the pool with nothing left gave %v", err)
	}
}
