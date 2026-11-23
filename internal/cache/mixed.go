package cache

import (
	"context"

	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
)

var ErrMixed = errors.New("cache: the cached content of the file is of another version")

type check struct {
	md5  string
	done chan struct{}
	err  error
}

func (ch *check) over() bool {
	select {
	case <-ch.done:
		return true
	default:
		return false
	}
}

func (c *Cache) startCheck(g *gen, f File) *check {
	if ch := g.check; ch != nil && (ch.md5 == f.MD5 || !ch.over()) {
		return ch
	}
	if !checkable(g, f) {
		return nil
	}
	ch := &check{md5: f.MD5, done: make(chan struct{})}
	g.check = ch

	g.refs++
	c.wg.Go(func() { c.finishCheck(g, f, ch) })
	return ch
}

func checkable(g *gen, f File) bool {
	return f.MD5 != "" && f.Size != 0 && f.Base == f.Size && !g.local && !g.stale && g.pieces.covers(span{0, f.Size})
}

func (c *Cache) finishCheck(g *gen, f File, ch *check) {
	mixed := false
	if it, err := c.opt.Records.ByID(f.ID); err != nil || it.MD5 != f.MD5 || it.Size != f.Size {

		ch.err = ErrStale
	} else {
		mixed = c.runCheck(g, f, ch)
	}
	c.unref(g)
	close(ch.done)

	if mixed && c.opt.Mixed != nil {
		c.opt.Mixed(c.bg, f)
	}
}

func (c *Cache) runCheck(g *gen, f File, ch *check) bool {
	sum, err := c.sum(f)
	if !c.isCurrent(g, f.Ino) {

		return false
	}
	if err != nil {
		ch.err = err
		if c.bg.Err() == nil {
			c.log.Warn("check the cached content of a file", "id", f.ID, "err", err)
		}
		return false
	}
	if sum == f.MD5 {
		return false
	}
	c.log.Warn("the cached content of a file is of another version than its md5 tells",
		"id", f.ID, "md5", f.MD5, "cached", sum)
	ch.err = ErrMixed
	return true
}

const sumBuf = 1 << 20

func (c *Cache) sum(f File) (string, error) {
	fd, err := os.Open(c.path(f.ID))
	if err != nil {
		return "", fmt.Errorf("cache: %w", err)
	}
	defer fd.Close()

	h := md5.New()
	buf := make([]byte, sumBuf)
	for left := f.Size; left > 0; {
		if err := c.bg.Err(); err != nil {
			return "", err
		}
		n, err := io.ReadFull(fd, buf[:min(int64(len(buf)), left)])
		if err != nil {
			return "", fmt.Errorf("cache: %s: %w", f.ID, err)
		}
		h.Write(buf[:n])
		left -= int64(n)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (c *Cache) Check(ctx context.Context, f File) error {
	g, err := c.current(f.Ino)
	if err != nil {
		return err
	}
	if f.Gen != 0 && f.Gen != g.num {
		return ErrStale
	}
	c.mu.Lock()
	ch := c.startCheck(g, f)
	c.mu.Unlock()
	if ch == nil {
		return nil
	}
	if ch.md5 != f.MD5 {

		return ErrStale
	}
	select {
	case <-ch.done:
		return ch.err
	case <-ctx.Done():
		return ctx.Err()
	}
}
