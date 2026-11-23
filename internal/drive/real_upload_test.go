//go:build realdrive

package drive_test

import (
	"bytes"
	"errors"
	"io"
	"net/url"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

type cutAfter struct {
	r io.Reader
	n int
}

var errCutContent = errors.New("the content changed while it went")

func (c *cutAfter) Read(p []byte) (int, error) {
	if c.n <= 0 {
		return 0, errCutContent
	}
	if len(p) > c.n {
		p = p[:c.n]
	}
	n, err := c.r.Read(p)
	c.n -= n
	return n, err
}

func TestRealCutSimpleUploadLeavesNoFile(t *testing.T) {
	r := testenv.RealDrive(t)
	ids, err := r.Drive.GenerateIDs(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	content := bytes.Repeat([]byte("x"), 64<<10)
	_, err = r.Drive.CreateFile(t.Context(), ids[0], "cut.bin", r.Folder, "application/octet-stream", time.Now(),
		int64(len(content)), func() io.Reader { return &cutAfter{r: bytes.NewReader(content), n: 16 << 10} })
	t.Logf("the cut upload ends with %v", err)
	if err == nil {
		t.Fatal("the cut upload passed")
	}
	var list struct{ Files []item }
	q := url.Values{"q": {"'" + r.Folder + "' in parents"}, "fields": {"files(id)"}}
	if code, body := r.GetJSON("files", q, &list); code != 200 {
		t.Fatalf("files.list answered %d: %s", code, body)
	}
	if len(list.Files) != 0 {
		t.Fatalf("after the cut upload the folder holds %d files", len(list.Files))
	}
}

func TestRealReadOnlyFileCannotBeModified(t *testing.T) {
	r := testenv.RealDrive(t)
	file := makeFile(t, r, "readonly.txt", r.Folder)
	restrict := map[string]any{"contentRestrictions": []map[string]any{{"readOnly": true, "reason": "fastgd test"}}}
	if code, body := r.Call("PATCH", "files/"+file, nil, restrict, nil); code != 200 {
		t.Fatalf("setting readOnly answered %d: %s", code, body)
	}
	var got item
	if code, body := r.GetJSON("files/"+file, url.Values{"fields": {"id," + rightFields}}, &got); code != 200 {
		t.Fatalf("files.get answered %d: %s", code, body)
	}
	t.Logf("files.get of an own readOnly file: %s", show(got.Capabilities))
	if c := got.Capabilities; c == nil || c.CanModifyContent == nil || *c.CanModifyContent {
		t.Errorf("a readOnly file has the rights %s, want canModifyContent false", show(c))
	}
	content := []byte("changed")
	_, err := r.Drive.UpdateContent(t.Context(), file, "text/plain", time.Now(), int64(len(content)),
		func() io.Reader { return bytes.NewReader(content) })
	t.Logf("a change of the content of a readOnly file ends with %v", err)
	if err == nil {
		t.Fatal("Drive took a change of the content of a readOnly file")
	}
}
