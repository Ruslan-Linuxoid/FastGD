package core

import (
	"context"
	"sync"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
)

func (f *FS) Run(ctx context.Context) {

	ctx = drive.InBackground(ctx)
	var lines sync.WaitGroup
	lines.Go(func() { f.pollDrive(ctx) })
	lines.Go(func() { f.watchQuota(ctx) })
	lines.Go(func() { f.refillIDs(ctx) })
	defer lines.Wait()
	defer f.bg.Wait()
	seconds := time.NewTicker(time.Second)
	defer seconds.Stop()
	usage := time.NewTicker(usageEvery)
	defer usage.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-seconds.C:
			f.everySecond(ctx)
		case <-usage.C:
			f.saveUsage()
			f.bg.Go(func() {
				f.edits.UnkeepIdle(ctx)
			})
		}
	}
}

func (f *FS) refillIDs(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-f.idsWanted:
			if err := f.fillIDs(ctx); f.tells(ctx, err) {
				f.log.Warn("top up fileIds", "err", err)
			}
		}
	}
}

func (f *FS) everySecond(ctx context.Context) {
	f.edits.SaveSizes()
	f.warnDirtyOver()
	f.starting.Lock()
	defer f.starting.Unlock()
	if f.draining.Load() {
		return
	}
	f.startDue(ctx)
	f.readGone(ctx)
	f.edits.StartBases(ctx, func(fetch func()) {
		f.bg.Go(func() {
			fetch()
		})
	})
}
