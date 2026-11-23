package core

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
)

type goneRevisions struct {
	mu sync.Mutex

	ids map[string]*goneReading

	held map[string]string
}

type goneReading struct {
	rev string

	due  time.Time
	wait time.Duration

	running bool
}

const (
	goneWaitFirst = time.Second
	goneWaitMax   = 5 * time.Minute
)

func (f *FS) heldRevision(id, rev string) bool {
	f.gone.mu.Lock()
	defer f.gone.mu.Unlock()
	return rev != "" && f.gone.held[id] == rev
}

func (f *FS) revisionGone(id, rev string) {
	f.gone.mu.Lock()
	defer f.gone.mu.Unlock()
	if f.gone.held[id] == rev || f.gone.ids[id] != nil {
		return
	}
	if f.gone.ids == nil {
		f.gone.ids = map[string]*goneReading{}
	}
	f.gone.ids[id] = &goneReading{rev: rev, wait: goneWaitFirst}
}

func (f *FS) readGone(ctx context.Context) {
	now := f.clock.Now()
	f.gone.mu.Lock()
	defer f.gone.mu.Unlock()
	for id, r := range f.gone.ids {
		if r.running || now.Before(r.due) {
			continue
		}
		r.running = true
		f.bg.Go(func() { f.readGoneOne(ctx, id, r) })
	}
}

func (f *FS) readGoneOne(ctx context.Context, id string, r *goneReading) {
	err := f.readAnew(ctx, id)
	f.gone.mu.Lock()
	defer f.gone.mu.Unlock()
	r.running = false
	if err != nil && ctx.Err() == nil && drive.Passing(err) {
		r.due = f.clock.Now().Add(max(r.wait, drive.WaitAsked(err)))
		r.wait = min(r.wait*2, goneWaitMax)
		return
	}
	delete(f.gone.ids, id)
	switch {
	case errors.Is(err, drive.ErrNotFound):
		f.log.Warn("a file whose revision went is gone from Drive", "id", id)
	case err != nil && ctx.Err() == nil:
		f.log.Warn("read anew a file whose revision Drive no longer holds", "id", id, "err", err)
	case err == nil:
		f.holdIfStill(id, r.rev)
	}
}

func (f *FS) revisionRefused(id, rev string, err error) {
	f.gone.mu.Lock()
	defer f.gone.mu.Unlock()
	if f.gone.held[id] == rev {
		return
	}
	if f.gone.held == nil {
		f.gone.held = map[string]string{}
	}
	f.gone.held[id] = rev
	f.log.Warn("Drive refuses the account a revision of a file: the file downloads by its fileId",
		"id", id, "revision", rev, "err", err)
}

func (f *FS) holdIfStill(id, rev string) {
	if it, err := f.store.ByID(id); err != nil || it.Rev != rev {
		return
	}
	if f.gone.held == nil {
		f.gone.held = map[string]string{}
	}
	f.gone.held[id] = rev
	f.log.Warn("Drive names as the head of a file a revision it does not give: the file downloads by its fileId",
		"id", id, "revision", rev)
}
