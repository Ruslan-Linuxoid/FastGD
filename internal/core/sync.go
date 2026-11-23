package core

import (
	"context"
	"sync"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

const (
	maxUploads = 4

	minBackoff = 5 * time.Second
	maxBackoff = 15 * time.Minute

	md5Alarm = 3

	refusedTries = 5

	readyBatch = 100

	idLow  = 200
	idFill = 1000
)

func (f *FS) syncRound(ctx context.Context) int {
	var wg sync.WaitGroup
	n := f.startRound(ctx, 0, &wg)
	wg.Wait()
	return n
}

func (f *FS) startRound(ctx context.Context, emptyIdle time.Duration, running *sync.WaitGroup) int {

	f.liftIdleRefusal()
	ops := f.due(emptyIdle)
	for _, op := range ops {
		f.launch(ctx, op, running)
	}
	return len(ops)
}

func (f *FS) launch(ctx context.Context, op meta.Op, running *sync.WaitGroup) {
	running.Go(func() {
		select {
		case f.upload <- struct{}{}:
		case <-ctx.Done():
			f.queue.requeue(op)
			f.finished(op.Ino)
			return
		}
		defer func() { <-f.upload }()
		f.send(ctx, op)
	})
}

const StopGrace = 120 * time.Second

func (f *FS) Drain(grace time.Duration) bool {
	f.starting.Lock()
	f.draining.Store(true)
	f.starting.Unlock()
	done := make(chan struct{})
	go func() {
		f.bg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(grace):
		return false
	}
}

func (f *FS) startDue(ctx context.Context) {
	f.startRound(ctx, emptyIdleUpload, &f.bg)
}

func (f *FS) due(emptyIdle time.Duration) []meta.Op {
	now := f.clock.Now()
	return f.queue.due(now, f.edits.Busy(now, emptyIdle))
}

func (f *FS) finished(ino meta.Ino) {
	f.queue.release(ino)
	f.forgetUnused(ino)
}
