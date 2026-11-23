package edited

import (
	"bytes"
	"context"
	"errors"
	"math/rand/v2"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func TestEditOfAFileFromDriveKeepsItsBase(t *testing.T) {
	d := &inDrive{id: "id-a", content: make([]byte, 3<<20)}
	rng := rand.New(rand.NewPCG(1, 2))
	for i := range d.content {
		d.content[i] = byte(rng.Uint32())
	}
	k := openOver(t, t.TempDir(), d, d.stream, nil)
	it := k.fromDrive(t, d)
	if err := k.MakeDirty(it); err != nil {
		t.Fatal(err)
	}
	it = k.item(t, it.Ino)
	want := bytes.Clone(d.content)
	patch := []byte("written in the middle of a page")
	copy(want[1<<20+100:], patch)
	if err := k.Write(context.Background(), it, 1<<20+100, patch); err != nil {
		t.Fatal(err)
	}
	now := requireWriteKeptTheBase(t, k, d, it, want)
	requireReadAroundTheWrite(t, k, now, want)
	requireBaseFetchedWithoutDrive(t, k, d, now, want)

	if out := k.uploaded(t, k.item(t, it.Ino)); out != Cleaned {
		t.Fatalf("the upload ended as %v", out)
	}
	if now := k.item(t, it.Ino); now.Dirty || now.Base != 0 || k.Bytes() != 0 {
		t.Fatalf("after the upload the file is dirty %v with a base of %d bytes, and %d bytes wait", now.Dirty, now.Base,
			k.Bytes())
	}
}

func requireWriteKeptTheBase(t *testing.T, k kept, d *inDrive, it meta.Item, want []byte) meta.Item {
	t.Helper()
	now := k.item(t, it.Ino)
	if !now.Dirty || now.Base != int64(len(want)) || k.Bytes() != int64(len(want)) {
		t.Fatalf("after the write the file is dirty %v with a base of %d bytes, and %d bytes wait", now.Dirty, now.Base,
			k.Bytes())
	}
	if downloads, _ := d.counts(); downloads == 0 || downloads > 2 {
		t.Fatalf("the write downloaded %d times, want the page around it", downloads)
	}
	return now
}

func requireReadAroundTheWrite(t *testing.T, k kept, now meta.Item, want []byte) {
	t.Helper()
	got := make([]byte, 8192)
	if _, err := k.Read(t.Context(), now, got, 1<<20); err != nil || !bytes.Equal(got, want[1<<20:1<<20+8192]) {
		t.Fatalf("the read around the write differs: %v", err)
	}
}

func requireBaseFetchedWithoutDrive(t *testing.T, k kept, d *inDrive, now meta.Item, want []byte) {
	t.Helper()
	got := make([]byte, 8192)
	d.set(true)
	if _, err := k.Read(t.Context(), now, got, 2<<20); err == nil {
		t.Fatal("bytes that were never fetched read without Drive")
	}
	k.FillBases(t.Context())
	d.set(false)
	if err := k.FetchBase(t.Context(), now.Ino); err != nil {
		t.Fatal(err)
	}
	downloads, asked := d.counts()
	if got := k.whole(t, now.Ino); !bytes.Equal(got, want) {
		t.Fatal("the edited file does not read as its version in Drive with the write")
	}
	if lacks, err := k.LacksBase(t.Context(), k.Local(now)); err != nil || lacks {
		t.Fatalf("bytes of the base are still to fetch: %v", err)
	}
	if after, askedAfter := d.counts(); after != downloads || askedAfter != asked {
		t.Fatalf("reading the whole file asked Drive %d more times for bytes", after-downloads)
	}
}

func TestUploadsThatDoNotMakeTheFileClean(t *testing.T) {
	k := open(t)
	it := k.created(t, "id-a", "a.bin", make([]byte, 5000))
	st := k.State(it)
	gen, size, mtime := st.Snapshot()
	up := Upload{Item: it, Gen: gen, Size: size, Remote: meta.Remote{MD5: "other", Version: 2}, SentMD5: "sum",
		SentMeta: meta.Sent{Name: it.Name, Parent: it.Parent, Mtime: mtime}}
	out, err := k.Record(st, up)
	if out != Kept || !errors.Is(err, ErrMD5) || st.Pending() != nil || st.Damaged() != 1 {
		t.Fatalf("an upload stored damaged ended as %v, %v", out, err)
	}
	if now := k.item(t, it.Ino); !now.Dirty || !now.RExists || k.Bytes() != 5000 {
		t.Fatalf("after it the file is dirty %v, in Drive %v, with %d bytes waiting", now.Dirty, now.RExists, k.Bytes())
	}
	up.Remote.MD5 = "sum"
	requireUnrecordedUploadWaits(t, k, st, up)
}

func requireUnrecordedUploadWaits(t *testing.T, k kept, st *State, up Upload) {
	t.Helper()

	fail := errors.New("the database is locked")
	k.BeforeRecord(func() error { return fail })
	if out, err := k.Record(st, up); out != Kept || !errors.Is(err, fail) || st.Pending() == nil {
		t.Fatalf("an upload the database did not record ended as %v, %v, kept %v", out, err, st.Pending() != nil)
	}
	k.BeforeRecord(nil)
	if out, err := k.Record(st, *st.Pending()); out != Cleaned || err != nil || st.Pending() != nil || k.Bytes() != 0 {
		t.Fatalf("recorded on the next round it ended as %v, %v", out, err)
	}
}
