package cache

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"golang.org/x/sys/unix"
)

func (c *Cache) fill(ctx context.Context, g *gen, f File, fl *fetch) error {
	sl := &prefetchSlot{c: c, fl: fl}
	defer sl.leave()
	if err := sl.enter(ctx); err != nil {
		return err
	}
	if fl.prefetch && c.lowDisk.Load() {
		return ErrLowDisk
	}
	n := PageUp(fl.len()) + page()
	c.take(n)
	fl.reserved.Store(n)
	fd, err := c.openPiece(g, f)
	if err != nil {
		return err
	}
	fl.setFD(fd)
	sk := &sink{c: c, f: f, fl: fl, fd: fd, pos: fl.off}
	err = c.pull(ctx, sl, sk)
	if sk.pos > fl.off {
		c.meter.Leave(time.Now())
	}
	if err == nil && sk.pos != fl.end {
		err = fmt.Errorf("cache: %s at %d: got %d bytes, want %d", f.ID, fl.off, sk.pos-fl.off, fl.len())
	}
	return err
}

type prefetchSlot struct {
	c  *Cache
	fl *fetch

	held bool
}

func (sl *prefetchSlot) enter(ctx context.Context) error {
	if !sl.fl.prefetch {
		return nil
	}
	select {
	case sl.c.sem <- struct{}{}:
		sl.held = true
	case <-sl.fl.wanted:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

func (sl *prefetchSlot) leave() {
	if sl.held {
		<-sl.c.sem
		sl.held = false
	}
}

type sink struct {
	c   *Cache
	f   File
	fl  *fetch
	fd  *os.File
	pos int64
}

func (sk *sink) write(b []byte) error {
	n := int64(len(b))
	if n > sk.fl.end-sk.pos {
		return fmt.Errorf("cache: %s at %d: more bytes than asked for", sk.f.ID, sk.fl.off)
	}
	if _, err := sk.fd.WriteAt(b, sk.pos); err != nil {
		return fmt.Errorf("cache: write %s at %d: %w", sk.f.ID, sk.pos, err)
	}
	sk.c.meter.Arrive(len(b), sk.pos == sk.fl.off, time.Now())
	sk.pos += n
	sk.fl.progress(n)
	return nil
}

func (c *Cache) pull(ctx context.Context, sl *prefetchSlot, sk *sink) error {
	fl := sl.fl
	for wait, slept := TaskRetryFirst, time.Duration(0); ; wait = min(wait*2, TaskRetryMax) {
		err := c.opt.Source(c.meter.Trace(ctx), sk.f.ID, sk.f.Rev, sk.pos, fl.end-sk.pos, sk.write)
		again := err != nil && fl.prefetch && !fl.isWanted() && slept < TaskRetryMax && sk.pos < fl.end &&
			c.opt.Passing != nil && c.opt.Passing(err)
		if !again {
			return err
		}
		sl.leave()
		if cerr := sl.pause(ctx, wait); cerr != nil {
			return cerr
		}
		slept = wait
	}
}

func (sl *prefetchSlot) pause(ctx context.Context, wait time.Duration) error {
	t := time.NewTimer(wait)
	select {
	case <-t.C:
	case <-sl.fl.wanted:
	case <-ctx.Done():
	}
	t.Stop()
	if err := ctx.Err(); err != nil {
		return err
	}
	return sl.enter(ctx)
}

func (c *Cache) openPiece(g *gen, f File) (*os.File, error) {
	p := c.path(f.ID)
	c.takePath(p)
	lock := c.fileLock(f.Ino)
	lock.RLock()
	if !c.isCurrent(g, f.Ino) {
		lock.RUnlock()
		return nil, errSwitched
	}

	fd, err := os.OpenFile(p, os.O_RDWR, 0)
	lock.RUnlock()
	if !errors.Is(err, fs.ErrNotExist) {
		return fd, err
	}
	lock.Lock()
	defer lock.Unlock()
	if !c.isCurrent(g, f.Ino) {
		return nil, errSwitched
	}
	fd, created, err := c.openForWrite(p)
	if err != nil || !created {
		return fd, err
	}
	if err := c.dropRecords(g, f.Ino); err != nil {
		fd.Close()
		return nil, err
	}
	return fd, nil
}

func (c *Cache) record(g *gen, f File, fl *fetch) error {
	if err := unix.Fdatasync(int(fl.fd.Fd())); err != nil {
		return fmt.Errorf("cache: sync %s: %w", f.ID, err)
	}

	lock := c.fileLock(f.Ino)
	lock.RLock()
	defer lock.RUnlock()
	if !c.isCurrent(g, f.Ino) {
		return errSwitched
	}
	same, err := samePath(fl.fd, c.path(f.ID))
	if err != nil {
		return err
	}
	if !same {
		return errReplaced
	}
	g.recMu.Lock()
	defer g.recMu.Unlock()
	if !g.synced {
		if err := syncDir(filepath.Dir(c.path(f.ID))); err != nil {
			return fmt.Errorf("cache: %w", err)
		}
		g.synced = true
	}
	if err := c.opt.Records.AddPiece(f.Ino, fl.off, fl.len()); err != nil {
		return err
	}
	c.mu.Lock()
	g.pieces.add(fl.span)
	c.worths.came(f.Ino, fl.span, fl.reads)
	g.drop(fl)
	c.noteFetching(g)
	fl.recorded = true
	join := len(g.pieces) > g.joinAt
	c.startCheck(g, f)

	extra := g.extentPageReserved
	g.extentPageReserved = true
	c.mu.Unlock()
	if extra {
		c.release(page())
	}
	if join {
		c.join(g, f)
	}
	return nil
}

func samePath(fd *os.File, path string) (bool, error) {
	var a, b unix.Stat_t
	if err := unix.Fstat(int(fd.Fd()), &a); err != nil {
		return false, fmt.Errorf("cache: %w", err)
	}
	err := unix.Stat(path, &b)
	if errors.Is(err, syscall.ENOENT) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("cache: %w", err)
	}
	return a.Dev == b.Dev && a.Ino == b.Ino, nil
}

func (c *Cache) join(g *gen, f File) {
	c.mu.Lock()
	before := len(g.pieces)
	joined := g.pieces.joined()
	c.mu.Unlock()
	g.joinAt = max(joinAbove, 2*len(joined))
	if len(joined) == before {
		return
	}
	rec := make([]meta.Piece, len(joined))
	for i, s := range joined {
		rec[i] = meta.Piece{Off: s.off, Len: s.len()}
	}
	if err := c.opt.Records.ReplacePieces(f.Ino, rec); err != nil {
		c.log.Warn("join cached pieces", "id", f.ID, "err", err)
		return
	}
	c.mu.Lock()
	g.pieces = joined
	c.worths.joined(f.Ino, joined)
	c.mu.Unlock()
	c.log.Warn("a file holds many cached pieces; its touching pieces are joined",
		"id", f.ID, "pieces", before, "joined", len(joined))
}
