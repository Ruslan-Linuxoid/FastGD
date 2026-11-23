package drive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
)

var ErrSessionGone = errors.New("drive: the upload session is gone")

var ErrSessionRefused = fmt.Errorf("%w: Drive refused a chunk for good", ErrSessionGone)

var errSessionNotOfDrive = fmt.Errorf("%w: the session address is not one of Drive", ErrSessionGone)

const statusResumeIncomplete = 308

type Progress struct {
	Confirmed int64
	Done      *File
}

func (c *Client) StartCreate(
	ctx context.Context, id, name, parent, mimeType string, modified time.Time, size int64,
) (string, error) {
	return c.startSession(ctx, http.MethodPost, "/upload/drive/v3/files",
		newItem(id, name, parent, mimeType, modified), mimeType, size, id)
}

func (c *Client) StartUpdate(ctx context.Context, id, mimeType string, modified time.Time, size int64) (string, error) {
	return c.startSession(ctx, http.MethodPatch, "/upload/drive/v3/files/"+url.PathEscape(id),
		&drive.File{ModifiedTime: contentStamp(modified)}, mimeType, size, id)
}

func (c *Client) startSession(
	ctx context.Context, method, path string, item *drive.File, mimeType string, size int64, id string,
) (string, error) {
	what := "start the upload of " + id
	body, err := json.Marshal(item)
	if err != nil {
		return "", fmt.Errorf("drive: %s: %w", what, err)
	}
	q := url.Values{"uploadType": {"resumable"}, "fields": {fileFields}}
	req, err := http.NewRequestWithContext(ctx, method, c.uploadBase+path+"?"+q.Encode(), bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("drive: %s: %w", what, err)
	}
	req.Header.Set("Content-Type", "application/json; charset=UTF-8")
	req.Header.Set("X-Upload-Content-Type", mimeType)
	req.Header.Set("X-Upload-Content-Length", strconv.FormatInt(size, 10))
	var loc string
	err = c.do(ctx, costUpload, func() error {
		req.Body = io.NopCloser(bytes.NewReader(body))
		resp, err := c.http.Do(req)
		if err != nil {
			return withoutURL(err)
		}
		defer resp.Body.Close()
		if err := googleapi.CheckResponse(resp); err != nil {
			return err
		}
		loc = resp.Header.Get("Location")
		return nil
	})
	if exists(err) {
		return "", ErrExists
	}
	if err != nil {
		return "", fmt.Errorf("drive: %s: %w", what, err)
	}
	if loc == "" {
		return "", fmt.Errorf("drive: %s: no session address in the answer", what)
	}
	if !c.trusted(loc) {

		return "", fmt.Errorf("drive: %s: the session address in the answer is not one of Drive", what)
	}
	return loc, nil
}

func (c *Client) SendChunk(ctx context.Context, session string, off, n, size int64, body io.Reader) (Progress, error) {
	if !c.trusted(session) {
		return Progress{}, errSessionNotOfDrive
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, session, c.throttled(ctx, body))
	if err != nil {
		return Progress{}, fmt.Errorf("drive: upload chunk: %w", err)
	}
	req.ContentLength = n
	req.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", off, off+n-1, size))
	var p Progress
	err = c.once(ctx, costChunk, func() (err error) {
		p, err = c.progress(req)
		return err
	})
	if err != nil && !errors.Is(err, ErrSessionGone) {
		return Progress{}, fmt.Errorf("drive: upload chunk at %d: %w", off, err)
	}
	return p, err
}

func (c *Client) UploadStatus(ctx context.Context, session string, size int64) (Progress, error) {
	if !c.trusted(session) {
		return Progress{}, errSessionNotOfDrive
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, session, http.NoBody)
	if err != nil {
		return Progress{}, fmt.Errorf("drive: upload status: %w", err)
	}
	req.Header.Set("Content-Range", fmt.Sprintf("bytes */%d", size))
	p, err := get(ctx, c, costOtherWrite, func() (Progress, error) {
		return c.progress(req)
	})
	if err != nil && !errors.Is(err, ErrSessionGone) {
		return Progress{}, fmt.Errorf("drive: upload status: %w", err)
	}
	return p, err
}

func withoutURL(err error) error {
	if uerr, ok := errors.AsType[*url.Error](err); ok {
		return &url.Error{Op: uerr.Op, URL: "the upload session", Err: uerr.Err}
	}
	return err
}

func (c *Client) progress(req *http.Request) (Progress, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return Progress{}, withoutURL(err)
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case statusResumeIncomplete:
		confirmed, err := confirmedBytes(resp.Header.Get("Range"))
		if err != nil {
			return Progress{}, err
		}
		return Progress{Confirmed: confirmed}, nil
	case http.StatusNotFound, http.StatusGone:
		return Progress{}, ErrSessionGone
	}
	if err := googleapi.CheckResponse(resp); err != nil {
		if sessionOver(resp.StatusCode) {
			return Progress{}, fmt.Errorf("%w: %w", ErrSessionRefused, err)
		}
		return Progress{}, err
	}
	var f drive.File
	if err := json.NewDecoder(resp.Body).Decode(&f); err != nil {
		return Progress{}, err
	}
	done, err := convert(&f)
	if err != nil {
		return Progress{}, err
	}
	return Progress{Done: &done}, nil
}

func sessionOver(status int) bool {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden, http.StatusRequestTimeout, http.StatusTooManyRequests:
		return false
	}
	return status >= 400 && status < 500
}

func confirmedBytes(h string) (int64, error) {
	if h == "" {
		return 0, nil
	}
	spec, ok := strings.CutPrefix(h, "bytes=0-")
	if !ok {
		return 0, fmt.Errorf("bad Range %q", h)
	}
	last, err := strconv.ParseInt(spec, 10, 64)
	if err != nil || last < 0 {
		return 0, fmt.Errorf("bad Range %q", h)
	}
	return last + 1, nil
}
