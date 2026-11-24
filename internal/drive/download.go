package drive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"
)

const maxResumes = 5

const streamChunk = 64 << 10

var errCut = errors.New("connection cut in the middle of the body")

var errStalled = errors.New("the body of the answer went silent")

type idleBody struct {
	body   io.Reader
	idle   time.Duration
	cancel context.CancelFunc
	timer  *time.Timer
	fired  atomic.Bool
}

func (c *Client) watch(body io.Reader, cancel context.CancelFunc) *idleBody {
	return &idleBody{body: body, idle: c.pace.lim.IdleRead, cancel: cancel}
}

func (c *Client) stalled(err error) error {
	c.pace.note(offline)
	return err
}

func (b *idleBody) Read(p []byte) (int, error) {
	if b.timer == nil {
		b.timer = time.AfterFunc(b.idle, func() {
			b.fired.Store(true)
			b.cancel()
		})
	} else {
		b.timer.Reset(b.idle)
	}
	n, err := b.body.Read(p)
	b.timer.Stop()

	if err != nil && err != io.EOF && b.fired.Load() {
		return n, errStalled
	}
	return n, err
}

func (c *Client) Stream(ctx context.Context, id string, off, length int64, sink func([]byte) error) error {
	return c.stream(ctx, id, off, length, sink, func(ctx context.Context, rng string) (*http.Response, error) {

		call := c.svc.Files.Get(id).AcknowledgeAbuse(true).SupportsAllDrives(true).Context(ctx)
		call.Header().Set("Range", rng)
		return call.Download()
	})
}

type media func(ctx context.Context, rng string) (*http.Response, error)

func (c *Client) stream(
	ctx context.Context, what string, off, length int64, sink func([]byte) error, from media,
) error {
	if length <= 0 {
		return nil
	}
	buf := make([]byte, min(length, streamChunk))
	var got int64
	total := int64(-1)
	for cuts := 0; got < length; {
		n, size, err := c.streamFrom(ctx, what, from, off+got, length-got, total, buf, sink)
		total = size
		got += n
		if err == nil {

			continue
		}
		if !errors.Is(err, errCut) || cuts == maxResumes || ctx.Err() != nil {
			return err
		}
		cuts++
	}
	return nil
}

func (c *Client) streamFrom(
	ctx context.Context, what string, from media, off, length, total int64, buf []byte, sink func([]byte) error,
) (int64, int64, error) {

	reqCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	resp, err := get(ctx, c, costDownload, func() (*http.Response, error) {
		return from(reqCtx, fmt.Sprintf("bytes=%d-%d", off, off+length-1))
	})
	if err != nil {
		return 0, total, downloadErr(what, off, err)
	}
	defer resp.Body.Close()
	size, given, err := answeredRange(what, resp, off, length)
	if err != nil {
		return 0, total, err
	}
	if size >= 0 {
		if total >= 0 && size != total {
			return 0, total, fmt.Errorf("drive: download %s: size changed from %d to %d mid-download", what, total, size)
		}
		total = size
		if off+length > size {
			return 0, total, fmt.Errorf("drive: download %s: the file ends at %d, before %d", what, size, off+length)
		}
	}
	sent, err := c.copyBody(ctx, what, c.watch(resp.Body, cancel), buf, off, given, sink)
	return sent, total, err
}

func answeredRange(what string, resp *http.Response, off, length int64) (size, given int64, err error) {
	given = length
	switch {
	case resp.StatusCode == http.StatusPartialContent:
		from, to, t, ok := parseContentRange(resp.Header.Get("Content-Range"))
		if !ok || from != off {
			return 0, 0, fmt.Errorf(downloadAt+"bad Content-Range %q", what, off, resp.Header.Get("Content-Range"))
		}
		return t, min(length, to-from+1), nil
	case resp.StatusCode == http.StatusOK && off == 0:
		return resp.ContentLength, given, nil
	default:

		return 0, 0, fmt.Errorf(downloadAt+"status %d without the range", what, off, resp.StatusCode)
	}
}

func (c *Client) copyBody(
	ctx context.Context, what string, body io.Reader, buf []byte, off, given int64, sink func([]byte) error,
) (int64, error) {
	var sent int64
	for sent < given {
		n, rerr := body.Read(buf[:min(int64(len(buf)), given-sent)])
		if n > 0 {
			c.bytes.Add(int64(n))
			if err := throttle(ctx, c.down, n); err != nil {
				return sent, downloadErr(what, off+sent, err)
			}
			if err := sink(buf[:n]); err != nil {
				return sent, err
			}
			sent += int64(n)
		}
		if errors.Is(rerr, errStalled) {
			return sent, c.stalled(downloadErr(what, off+sent, errStalled))
		}
		if errors.Is(rerr, io.EOF) && sent < given {
			return sent, downloadErr(what, off+sent, errCut)
		}
		if rerr != nil && !errors.Is(rerr, io.EOF) {
			return sent, downloadErr(what, off+sent, fmt.Errorf("%w: %w", errCut, rerr))
		}
	}
	return sent, nil
}

func parseContentRange(h string) (from, to, total int64, ok bool) {
	if n, err := fmt.Sscanf(h, "bytes %d-%d/%d", &from, &to, &total); n != 3 || err != nil || to < from {
		return 0, 0, 0, false
	}
	return from, to, total, true
}

const downloadAt = "drive: download %s at %d: "

func downloadErr(what string, off int64, err error) error {
	return fmt.Errorf(downloadAt+"%w", what, off, err)
}
