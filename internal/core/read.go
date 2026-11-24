package core

import (
	"context"
	"errors"
	"sync"
	"syscall"

	"github.com/Ruslan-Linuxoid/FastGD/internal/cache"
	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

type handle struct {
	ino    meta.Ino
	writer bool

	mu   sync.RWMutex
	r    *cache.Reader
	page []byte
}

type handles struct {
	mu   sync.Mutex
	next uint64
	open map[uint64]*handle
}

func (h *handles) add(x *handle) uint64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.open == nil {
		h.open = map[uint64]*handle{}
	}
	h.next++
	h.open[h.next] = x
	return h.next
}

func (h *handles) get(fh uint64) (*handle, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	x, ok := h.open[fh]
	return x, ok
}

func (h *handles) remove(fh uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.open, fh)
}

func (h *handles) opened(ino meta.Ino) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, x := range h.open {
		if x.ino == ino {
			return true
		}
	}
	return false
}

const openTries = 3

func (f *FS) Open(ctx context.Context, ino uint64, flags uint32) (uint64, error) {
	for try := 1; ; try++ {
		fh, err := f.openOnce(ctx, ino, flags)
		if !errors.Is(err, cache.ErrStale) {
			return fh, err
		}
		if try >= openTries {
			return 0, f.eio("open", err, "ino", ino)
		}
	}
}

func (f *FS) openOnce(ctx context.Context, ino uint64, flags uint32) (uint64, error) {
	it, gen, err := f.settledFile(ino)
	if err != nil {
		return 0, err
	}
	if f.beforeOpen != nil {
		f.beforeOpen()
	}
	switch it.Kind {
	case meta.KindDir:
		return 0, syscall.EISDIR
	case meta.KindDoc:
		return f.openLink(it, flags)
	}
	if it, err = f.edits.TakenUp(it); err != nil {

		return 0, f.eio("open", err, "id", it.ID)
	}
	return f.open(ctx, it, gen, flags)
}

func (f *FS) open(ctx context.Context, it meta.Item, gen uint64, flags uint32) (uint64, error) {
	writer := isWrite(flags) || flags&syscall.O_TRUNC != 0
	if err := f.openAllowed(it, flags, writer); err != nil {
		return 0, err
	}
	file := f.edits.FileOf(it)
	file.Gen = gen
	r, err := f.cache.Open(file)
	if errors.Is(err, cache.ErrStale) {
		return 0, err
	}
	if err != nil {
		return 0, f.eio("open", err, "id", it.ID)
	}
	if writer {
		f.edits.AddWriter(it)
	}
	fh := f.handles.add(&handle{ino: it.Ino, r: r, writer: writer})
	if err := f.cutOnOpen(ctx, it, flags, file); err != nil {
		f.Release(fh)
		return 0, err
	}
	return fh, nil
}

func (f *FS) readLocal(ctx context.Context, it meta.Item, dest []byte, off int64) (int, error) {
	var n int
	err := f.edits.WithBase(ctx, it.Ino, func() (err error) {
		n, err = f.edits.ReadLocal(ctx, f.edits.Local(it), dest, off)
		return err
	})
	switch {
	case errors.Is(err, context.Canceled):
		return 0, syscall.EINTR
	case err != nil:
		return 0, f.eio("read", err, "ino", it.Ino, "off", off)
	}
	return n, nil
}

func (f *FS) Read(ctx context.Context, fh uint64, off int64, dest []byte) (int, error) {
	h, ok := f.handles.get(fh)
	if !ok {
		return 0, syscall.EBADF
	}
	if len(dest) == 0 {
		return 0, nil
	}
	if h.page != nil {
		return readPage(h.page, off, dest), nil
	}
	if h.writer {
		f.follow(h)
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	var file cache.File
	if !h.r.Stale() {
		var n int
		var done bool
		var err error
		if file, n, done, err = f.readFresh(ctx, h, dest, off); done {
			return n, err
		}
	}
	n, err := h.r.ReadAt(ctx, file, dest, off)
	if errors.Is(err, cache.ErrLocal) {

		it, err := f.item(uint64(h.ino))
		if err != nil {
			return 0, err
		}
		return f.readLocal(ctx, it, dest, off)
	}
	if err := f.readFailure(err, h.ino, off); err != nil {
		return 0, err
	}
	return n, nil
}

func (f *FS) follow(h *handle) {
	h.mu.RLock()
	stale := h.r.Stale()
	h.mu.RUnlock()
	if !stale {
		return
	}
	r, err := f.reopen(uint64(h.ino))
	if err != nil {

		return
	}
	h.mu.Lock()
	old := h.r
	h.r = r
	h.mu.Unlock()
	old.Close()
}

func (f *FS) reopen(ino uint64) (*cache.Reader, error) {
	for try := 1; ; try++ {
		it, gen, err := f.settledFile(ino)
		if err != nil {
			return nil, err
		}
		if f.beforeOpen != nil {
			f.beforeOpen()
		}
		file := f.edits.FileOf(it)
		file.Gen = gen
		r, err := f.cache.Open(file)
		if !errors.Is(err, cache.ErrStale) || try >= openTries {
			return r, err
		}
	}
}

func (f *FS) Release(fh uint64) {
	h, ok := f.handles.get(fh)
	if !ok {
		return
	}
	f.handles.remove(fh)
	h.mu.Lock()
	if h.r != nil {
		h.r.Close()
	}
	h.mu.Unlock()
	if h.writer {
		f.edits.CloseWriter(h.ino)
	}
	f.forgetUnused(h.ino)
}

func (f *FS) download(ctx context.Context, id, rev string, off, length int64, sink func([]byte) error) error {
	if f.heldRevision(id, rev) {
		rev = ""
	}
	var err error
	if rev != "" {
		took := false
		err = f.opt.Drive.StreamRevision(ctx, id, rev, off, length, func(b []byte) error {
			took = true
			return sink(b)
		})

		if !took && errors.Is(err, drive.ErrRevisionRefused) {
			f.revisionRefused(id, rev, err)
			rev = ""
		}
	}
	if rev == "" {
		err = f.opt.Drive.Stream(ctx, id, off, length, sink)
	}
	if rev != "" && errors.Is(err, drive.ErrNoRevision) {
		f.revisionGone(id, rev)
	}
	f.noteDownload(id, err)
	return err
}
