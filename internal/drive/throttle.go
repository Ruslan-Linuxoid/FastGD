package drive

import (
	"context"
	"io"

	"golang.org/x/time/rate"
)

type Speed struct {
	Download int64
	Upload   int64
}

const throttleBurst = streamChunk

func newThrottle(bytesPerSecond int64) *rate.Limiter {
	if bytesPerSecond <= 0 {
		return nil
	}
	return rate.NewLimiter(rate.Limit(bytesPerSecond), int(min(throttleBurst, bytesPerSecond)))
}

func throttle(ctx context.Context, l *rate.Limiter, n int) error {
	if l == nil {
		return nil
	}
	for n > 0 {
		part := min(n, l.Burst())
		if err := l.WaitN(ctx, part); err != nil {
			return err
		}
		n -= part
	}
	return nil
}

func (c *Client) throttled(ctx context.Context, r io.Reader) io.Reader {
	if c.up == nil {
		return r
	}
	return &throttledReader{ctx: ctx, r: r, l: c.up}
}

type throttledReader struct {
	ctx context.Context
	r   io.Reader
	l   *rate.Limiter
}

func (t *throttledReader) Read(p []byte) (int, error) {
	n, err := t.r.Read(p[:min(len(p), throttleBurst)])
	if werr := throttle(t.ctx, t.l, n); werr != nil {
		return 0, werr
	}
	return n, err
}
