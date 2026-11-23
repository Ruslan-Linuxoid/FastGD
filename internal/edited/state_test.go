package edited

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

type inDrive struct {
	mu        sync.Mutex
	id        string
	content   []byte
	downloads int
	asked     int
	down      bool
}

func TestBytesFollowTheFilesThatWaitForDrive(t *testing.T) {
	k := open(t)
	a := k.created(t, "id-a", "a.bin", make([]byte, 5000))
	b := k.created(t, "id-b", "b.bin", make([]byte, 300))
	if got := k.Bytes(); got != 5300 {
		t.Fatalf("two files made here: %d bytes wait, want 5300", got)
	}
	if err := k.Truncate(context.Background(), a, 1000); err != nil {
		t.Fatal(err)
	}
	if got := k.Bytes(); got != 1300 {
		t.Fatalf("after a cut to 1000: %d bytes wait, want 1300", got)
	}

	if err := k.store.Visible().Trash(b.Ino); err != nil {
		t.Fatal(err)
	}
	k.Recounted(b.Ino)
	if got := k.Bytes(); got != 1000 {
		t.Fatalf("with one file in the trash: %d bytes wait, want 1000", got)
	}
	requireBytesAfterUploads(t, k, a)
	requireBytesAfterGivingWay(t, k, a)
}

func requireBytesAfterUploads(t *testing.T, k kept, a meta.Item) {
	t.Helper()
	st := k.State(a)
	gen, size, mtime := st.Snapshot()
	if err := k.Write(context.Background(), k.item(t, a.Ino), 0, []byte("more")); err != nil {
		t.Fatal(err)
	}
	out, err := k.Record(st, Upload{Item: k.item(t, a.Ino), Gen: gen, Size: size, Remote: meta.Remote{MD5: "sum",
		Version: 2}, SentMD5: "sum",
		SentMeta: meta.Sent{Name: a.Name, Parent: a.Parent, Mtime: mtime}})
	if err != nil || out != Rewritten || k.Bytes() != 1000 {
		t.Fatalf("an upload overtaken by a write ended as %v, %v, with %d bytes waiting", out, err, k.Bytes())
	}
	if out := k.uploaded(t, k.item(t, a.Ino)); out != Cleaned || k.Bytes() != 0 {
		t.Fatalf("an upload of the whole file ended as %v, with %d bytes waiting", out, k.Bytes())
	}
	if it := k.item(t, a.Ino); it.Dirty || it.Size != 1000 {
		t.Fatalf("the uploaded file is dirty %v with %d bytes in the database", it.Dirty, it.Size)
	}
}

func requireBytesAfterGivingWay(t *testing.T, k kept, a meta.Item) {
	t.Helper()
	if err := k.Truncate(context.Background(), k.item(t, a.Ino), 400); err != nil {
		t.Fatal(err)
	}
	if got := k.Bytes(); got != 400 {
		t.Fatalf("changed again: %d bytes wait, want 400", got)
	}
	locked := k.Lock(k.item(t, a.Ino))
	k.GaveWay(a.Ino, locked)
	locked.Unlock()
	if got := k.Bytes(); got != 0 {
		t.Fatalf("after the file gave way to Drive's version: %d bytes wait", got)
	}
}

func TestChangeOfAStateBeingForgottenIsKept(t *testing.T) {
	k := open(t)
	content := bytes.Repeat([]byte("0123456789"), 500)
	it := k.created(t, "id-a", "a.bin", content)
	if out := k.uploaded(t, it); out != Cleaned {
		t.Fatalf("the upload ended as %v", out)
	}
	it = k.item(t, it.Ino)
	truncateWhileItsStateIsForgotten(t, k, it)
	if size, _, ok := k.Seen(it.Ino); !ok || size != 1000 {
		t.Fatalf("after the truncation the file shows %d bytes, changed here: %v", size, ok)
	}
	if now := k.item(t, it.Ino); !now.Dirty || now.Size != 1000 || k.Bytes() != 1000 {
		t.Fatalf("the database holds the file dirty %v with %d bytes, and %d bytes wait", now.Dirty, now.Size, k.Bytes())
	}

	if got := k.whole(t, it.Ino); !bytes.Equal(got, content[:1000]) {
		t.Fatalf("the file reads %d bytes, other than the 1000 it was cut to", len(got))
	}
	if out := k.uploaded(t, k.item(t, it.Ino)); out != Cleaned || k.Bytes() != 0 {
		t.Fatalf("the upload of the cut file ended as %v, with %d bytes waiting", out, k.Bytes())
	}
}

func truncateWhileItsStateIsForgotten(t *testing.T, k kept, it meta.Item) {
	t.Helper()

	found := k.State(it)
	found.mu.Lock()
	done := make(chan error, 1)
	go func() { done <- k.Truncate(context.Background(), it, 1000) }()
	time.Sleep(100 * time.Millisecond)
	k.smu.Lock()
	delete(k.states, it.Ino)
	k.smu.Unlock()
	found.released = true
	found.mu.Unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestWriterCountedFromAnOlderRowStillChangesTheFile(t *testing.T) {
	k := open(t)
	it := k.created(t, "id-a", "a.bin", make([]byte, 5000))
	read := k.item(t, it.Ino)
	if out := k.uploaded(t, read); out != Cleaned {
		t.Fatalf("the upload ended as %v", out)
	}
	k.AddWriter(read)
	if err := k.Truncate(context.Background(), read, 1000); err != nil {
		t.Fatal(err)
	}
	k.CloseWriter(read.Ino)
	if now := k.item(t, it.Ino); !now.Dirty || now.Size != 1000 {
		t.Fatalf("the change went into a file the database holds dirty %v with %d bytes", now.Dirty, now.Size)
	}
}

func TestAFileLockedForLongHoldsNoOther(t *testing.T) {
	k := open(t)
	slow := k.created(t, "id-a", "slow.bin", make([]byte, 100))
	other := k.created(t, "id-b", "other.bin", make([]byte, 100))
	st := k.Lock(slow)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if busy := k.Busy(time.Now().Add(time.Hour), time.Minute); len(busy) != 1 || busy[0] != slow.Ino {
			t.Errorf("the files in use are %v, want the locked one alone", busy)
		}
		k.SaveSizes()
		k.Recount()
		if _, _, ok := k.Seen(slow.Ino); !ok {
			t.Error("the locked file shows no size")
		}
		if err := k.Write(context.Background(), other, 100, []byte("more")); err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the work on the other files waits for the locked one")
	}

	if err := k.store.Visible().Trash(slow.Ino); err != nil {
		t.Fatal(err)
	}
	k.Recounted(slow.Ino)
	if got := k.Bytes(); got != 204 {
		t.Fatalf("with the trashed file still locked %d bytes wait, want 204", got)
	}
	st.Unlock()
	if got := k.Bytes(); got != 104 {
		t.Fatalf("after the lock was given back %d bytes wait, want 104", got)
	}
}

func TestStartTakesUpWhatTheLastRunLeft(t *testing.T) {
	dir := t.TempDir()
	k := openOver(t, dir, noDrive{t}, nil, nil)
	content := bytes.Repeat([]byte("abcdefgh"), 1000)
	it := k.created(t, "id-a", "a.bin", content)
	if err := k.Persist(it); err != nil {
		t.Fatal(err)
	}

	if err := k.writeLocal(t.Context(), k.Local(it), []byte("tail"), int64(len(content))); err != nil {
		t.Fatal(err)
	}
	want := append(bytes.Clone(content), "tail"...)
	k.clean.Close()
	k.store.Close()

	requireUnlookedFileWaitsThenIsTakenUp(t, dir, it, len(content), want)
	k = openOver(t, dir, noDrive{t}, nil, func(step string, _ meta.Item) error {
		if step == StepResume {
			return errors.New("the database cannot be read")
		}
		return nil
	})
	if err := k.Recover(); err == nil {
		t.Fatal("the start went on after the database failed")
	}
}

func requireUnlookedFileWaitsThenIsTakenUp(t *testing.T, dir string, it meta.Item, had int, want []byte) {
	t.Helper()
	failing := true
	fault := func(step string, _ meta.Item) error {
		if step == StepSize && failing {
			return syscall.EMFILE
		}
		return nil
	}
	k := openOver(t, dir, noDrive{t}, nil, fault)
	if err := k.Recover(); err != nil {
		t.Fatalf("the start fails for one file: %v", err)
	}
	if got := k.Bytes(); got != int64(had) {
		t.Fatalf("with the file not taken up %d bytes wait, want what the database has, %d", got, had)
	}
	if _, err := k.TakenUp(k.item(t, it.Ino)); !errors.Is(err, errNotTakenUp) {
		t.Fatalf("a file that cannot be looked at was taken up: %v", err)
	}
	failing = false
	now, err := k.TakenUp(k.item(t, it.Ino))
	if err != nil || now.Size != int64(len(want)) || k.Bytes() != int64(len(want)) {
		t.Fatalf("taken up, the file has %d bytes and %d wait, want %d: %v", now.Size, k.Bytes(), len(want), err)
	}
	if got := k.whole(t, it.Ino); !bytes.Equal(got, want) {
		t.Fatal("the file taken up reads different from what the disk held")
	}
	k.clean.Close()
	k.store.Close()
}
