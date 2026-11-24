package drive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync/atomic"
	"time"

	"google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
)

var ErrExists = errors.New("drive: an item with this fileId exists")

func (c *Client) GenerateIDs(ctx context.Context, n int) ([]string, error) {
	ids, err := get(ctx, c, costOtherWrite, func() (*drive.GeneratedIds, error) {
		return c.svc.Files.GenerateIds().Count(int64(n)).Space("drive").Type("files").Context(ctx).Do()
	})
	if err != nil {
		return nil, fmt.Errorf("drive: generate ids: %w", err)
	}
	for _, id := range ids.Ids {
		if !ValidID(id) {
			return nil, fmt.Errorf("drive: generate ids: %w", &BadIDError{ID: id})
		}
	}
	return ids.Ids, nil
}

func MimeFor(name string) string {
	if t := mime.TypeByExtension(path.Ext(name)); t != "" {
		return t
	}
	return "application/octet-stream"
}

func newItem(id, name, parent, mimeType string, modified time.Time) *drive.File {
	return &drive.File{
		Id:           id,
		Name:         name,
		Parents:      []string{parent},
		MimeType:     mimeType,
		ModifiedTime: contentStamp(modified),
	}
}

func stored(f *drive.File, err error, what string) (*File, error) {
	if exists(err) {
		return nil, ErrExists
	}
	if err != nil {
		return nil, fmt.Errorf("drive: %s: %w", what, err)
	}
	out, err := convert(f)
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func exists(err error) bool {
	return hasCode(err, http.StatusConflict)
}

func (c *Client) CreateFolder(ctx context.Context, id, name, parent string, modified time.Time) (*File, error) {
	f, err := get(ctx, c, costWrite, func() (*drive.File, error) {
		return c.svc.Files.Create(newItem(id, name, parent, MimeFolder, modified)).SupportsAllDrives(true).
			Fields(fileFields).Context(ctx).Do()
	})
	return stored(f, err, "create folder "+id)
}

func (c *Client) CreateFile(
	ctx context.Context, id, name, parent, mimeType string, modified time.Time, size int64, content func() io.Reader,
) (*File, error) {
	f, err := c.uploadWhole(ctx, http.MethodPost, "/upload/drive/v3/files",
		newItem(id, name, parent, mimeType, modified), mimeType, size, content)
	return stored(f, err, "create file "+id)
}

func (c *Client) UpdateContent(
	ctx context.Context, id, mimeType string, modified time.Time, size int64, content func() io.Reader,
) (*File, error) {
	f, err := c.uploadWhole(ctx, http.MethodPatch, "/upload/drive/v3/files/"+url.PathEscape(id),
		&drive.File{ModifiedTime: contentStamp(modified)}, mimeType, size, content)
	return stored(f, err, "update content of "+id)
}

func (c *Client) uploadWhole(
	ctx context.Context, method, at string, item *drive.File, mimeType string, size int64, content func() io.Reader,
) (*drive.File, error) {
	meta, err := json.Marshal(item)
	if err != nil {
		return nil, err
	}
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	boundary := multipart.NewWriter(io.Discard).Boundary()
	head := "--" + boundary + "\r\nContent-Type: application/json; charset=UTF-8\r\n\r\n" + string(meta) +
		"\r\n--" + boundary + "\r\nContent-Type: " + mimeType + "\r\n\r\n"
	tail := "\r\n--" + boundary + "--\r\n"
	q := url.Values{"uploadType": {"multipart"}, "fields": {fileFields}, "supportsAllDrives": {"true"}}
	return get(ctx, c, costUpload, func() (*drive.File, error) {
		media := &contentReader{r: c.throttled(ctx, content())}
		req, err := http.NewRequestWithContext(ctx, method, c.uploadBase+at+"?"+q.Encode(),
			io.MultiReader(strings.NewReader(head), media, strings.NewReader(tail)))
		if err != nil {
			return nil, err
		}
		req.ContentLength = int64(len(head)) + size + int64(len(tail))
		req.Header.Set("Content-Type", "multipart/related; boundary="+boundary)
		resp, err := c.http.Do(req)
		if cerr := media.err.Load(); cerr != nil {

			return nil, *cerr
		}
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if err := googleapi.CheckResponse(resp); err != nil {
			return nil, err
		}
		var f drive.File
		if err := json.NewDecoder(resp.Body).Decode(&f); err != nil {
			return nil, err
		}
		return &f, nil
	})
}

type contentReader struct {
	r   io.Reader
	err atomic.Pointer[error]
}

func (c *contentReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if err != nil && err != io.EOF {
		c.err.Store(&err)
	}
	return n, err
}

func stamp(t time.Time) string {
	if t.IsZero() {
		t = time.Unix(0, 0)
	}
	return t.UTC().Format(time.RFC3339Nano)
}

func contentStamp(t time.Time) string {
	if atBound(t) {
		return ""
	}
	return stamp(t)
}
