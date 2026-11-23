//go:build realdrive

package drive_test

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestRealOldRevisionAndKeepForever(t *testing.T) {
	r := testenv.RealDrive(t)
	file := makeFile(t, r, "revisions.txt", r.Folder)
	first, err := r.Drive.Get(t.Context(), file)
	if err != nil {
		t.Fatal(err)
	}
	old := first.HeadRevision
	content := []byte("the second content")
	second, err := r.Drive.UpdateContent(t.Context(), file, "text/plain", time.Now(), int64(len(content)),
		func() io.Reader { return bytes.NewReader(content) })
	if err != nil {
		t.Fatal(err)
	}
	if second.HeadRevision == old {
		t.Fatalf("scenario not built: the update kept the head revision %s", old)
	}
	kept, err := r.Drive.RevisionKept(t.Context(), file, old)
	t.Logf("the older revision is kept forever: %v, %v", kept, err)

	t.Logf("the content of the older revision without the mark: %v", readRevision(t, r, file, old))
	if err := r.Drive.KeepRevision(t.Context(), file, old, true); err != nil {
		t.Fatalf("the mark on the older revision: %v", err)
	}
	if err := readRevision(t, r, file, old); err != nil {
		t.Fatalf("the content of the older revision kept forever: %v", err)
	}
	t.Logf("the mark off the older revision: %v", r.Drive.KeepRevision(t.Context(), file, old, false))
	t.Logf("the mark on the head revision: %v", r.Drive.KeepRevision(t.Context(), file, second.HeadRevision, true))
	t.Logf("the mark off the head revision: %v", r.Drive.KeepRevision(t.Context(), file, second.HeadRevision, false))

	code, body := r.Call(http.MethodDelete, "files/"+file+"/revisions/"+old, nil, nil, nil)
	t.Logf("the deletion of the older revision answered %d %s", code, body)
	err = readRevision(t, r, file, old)
	t.Logf("the content of a deleted revision: %v", err)
	if code == http.StatusNoContent && !errors.Is(err, drive.ErrNoRevision) {
		t.Errorf("the content of a deleted revision ends with %v, want ErrNoRevision", err)
	}
	_, err = r.Drive.RevisionKept(t.Context(), file, old)
	t.Logf("the mark of a deleted revision: %v", err)
}

func readRevision(t *testing.T, r *testenv.Real, id, rev string) error {
	t.Helper()
	const want = "content of revisions.txt"
	var got []byte
	err := r.Drive.StreamRevision(t.Context(), id, rev, 0, int64(len(want)), func(b []byte) error {
		got = append(got, b...)
		return nil
	})
	if err == nil && string(got) != want {
		t.Errorf("the revision %s holds %q, want %q", rev, got, want)
	}
	return err
}

func TestRealOldRevisionRangeWithoutKeepForever(t *testing.T) {
	r := testenv.RealDrive(t)
	ids, err := r.Drive.GenerateIDs(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	old := make([]byte, 2<<20)
	for i := range old {
		old[i] = byte(i * 7)
	}
	f, err := r.Drive.CreateFile(t.Context(), ids[0], "big.bin", r.Folder, "application/octet-stream", time.Now(),
		int64(len(old)), func() io.Reader { return bytes.NewReader(old) })
	if err != nil {
		t.Fatal(err)
	}
	rev := f.HeadRevision
	next := bytes.Repeat([]byte("n"), 1<<20)
	if _, err := r.Drive.UpdateContent(t.Context(), f.ID, "application/octet-stream", time.Now(), int64(len(next)),
		func() io.Reader { return bytes.NewReader(next) }); err != nil {
		t.Fatal(err)
	}
	if kept, err := r.Drive.RevisionKept(t.Context(), f.ID, rev); err != nil || kept {
		t.Fatalf("scenario not built: the older revision is kept forever: %v, %v", kept, err)
	}
	const off, n = 1<<20 + 12345, 300 << 10
	var got []byte
	err = r.Drive.StreamRevision(t.Context(), f.ID, rev, off, n, func(b []byte) error {
		got = append(got, b...)
		return nil
	})
	if err != nil || !bytes.Equal(got, old[off:off+n]) {
		t.Fatalf("the range of the older revision without the mark: %d bytes, the same %v, %v",
			len(got), bytes.Equal(got, old[off:off+n]), err)
	}
}

func TestRealHeadRevisionRangeWithoutKeepForever(t *testing.T) {
	r := testenv.RealDrive(t)
	ids, err := r.Drive.GenerateIDs(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	content := pattern(2 << 20)
	f, err := r.Drive.CreateFile(t.Context(), ids[0], "head.bin", r.Folder, "application/octet-stream", time.Now(),
		int64(len(content)), func() io.Reader { return bytes.NewReader(content) })
	if err != nil {
		t.Fatal(err)
	}
	if kept, err := r.Drive.RevisionKept(t.Context(), f.ID, f.HeadRevision); err != nil || kept {
		t.Fatalf("scenario not built: the head revision is kept forever: %v, %v", kept, err)
	}
	const off, n = 1<<20 + 777, 200 << 10
	var got []byte
	err = r.Drive.StreamRevision(t.Context(), f.ID, f.HeadRevision, off, n, func(b []byte) error {
		got = append(got, b...)
		return nil
	})
	if err != nil || !bytes.Equal(got, content[off:off+n]) {
		t.Fatalf("the range of the head revision without the mark: %d bytes, the same %v, %v",
			len(got), bytes.Equal(got, content[off:off+n]), err)
	}
}

const foreignFile = "1NS0pSh3vCU71PFilgyv3guwEwoWYaJwi"

func TestRealHeadRevisionOfAForeignFile(t *testing.T) {
	r := testenv.RealDrive(t)
	var f struct {
		OwnedByMe      bool   `json:"ownedByMe"`
		HeadRevisionID string `json:"headRevisionId"`
		Capabilities   struct {
			CanReadRevisions *bool `json:"canReadRevisions"`
			CanEdit          *bool `json:"canEdit"`
			CanModifyContent *bool `json:"canModifyContent"`
		} `json:"capabilities"`
		Size string `json:"size"`
	}
	q := url.Values{"fields": {"ownedByMe,headRevisionId,size,capabilities(canReadRevisions,canEdit,canModifyContent)"}}
	if code, body := r.GetJSON("files/"+foreignFile, q, &f); code != http.StatusOK {
		t.Fatalf("files.get answered %d: %s", code, body)
	}
	b := func(v *bool) string {
		if v == nil {
			return "-"
		}
		return strconv.FormatBool(*v)
	}
	t.Logf("ownedByMe %v, head %s, size %s, canReadRevisions %s, canEdit %s, canModifyContent %s", f.OwnedByMe,
		f.HeadRevisionID, f.Size, b(f.Capabilities.CanReadRevisions), b(f.Capabilities.CanEdit),
		b(f.Capabilities.CanModifyContent))
	if f.OwnedByMe || f.HeadRevisionID == "" {
		t.Fatalf("scenario not built: the file is the account's own, or names no head revision")
	}
	var got []byte
	err := r.Drive.StreamRevision(t.Context(), foreignFile, f.HeadRevisionID, 0, 16, func(p []byte) error {
		got = append(got, p...)
		return nil
	})
	t.Logf("revisions.get alt=media of the head revision: %d bytes, %v", len(got), err)
	var whole []byte
	err = r.Drive.Stream(t.Context(), foreignFile, 0, 16, func(p []byte) error {
		whole = append(whole, p...)
		return nil
	})
	t.Logf("files.get alt=media: %d bytes, %v", len(whole), err)
}
