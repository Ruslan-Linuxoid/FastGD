package core

import (
	"context"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
)

const pollInterval = 10 * time.Second

const tickSlack = 10 * time.Millisecond

func (f *FS) pollDrive(ctx context.Context) {
	seconds := time.NewTicker(time.Second)
	defer seconds.Stop()
	poll := func() {
		f.loadRights(ctx)
		if err := f.PollChanges(ctx); f.tells(ctx, err) {
			f.log.Warn("poll changes", "err", err)
		}
	}
	polled := f.clock.Now()
	poll()
	for {
		select {
		case <-ctx.Done():
			return
		case <-seconds.C:

			now := f.clock.Now()
			if polled.After(now) {

				polled = now
			}
			if now.Sub(polled) >= pollInterval-tickSlack {
				if polled = polled.Add(pollInterval); now.Sub(polled) >= time.Second {
					polled = now
				}
				poll()
				if end := f.clock.Now(); end.Sub(polled) >= pollInterval {
					polled = end
				}
			}
		}
	}
}

func (f *FS) watchQuota(ctx context.Context) {
	seconds := time.NewTicker(time.Second)
	defer seconds.Stop()
	quotas := time.NewTicker(quotaInterval)
	defer quotas.Stop()
	probes := time.NewTicker(probeInterval)
	defer probes.Stop()
	quota := func() {
		f.quotaStale.Store(false)
		if err := f.refreshQuota(ctx); f.tells(ctx, err) {
			f.log.Warn("read the Drive quota", "err", err)
		}
	}
	quota()
	for {
		select {
		case <-ctx.Done():
			return
		case <-seconds.C:
			if f.quotaStale.Load() {
				quota()
			}
		case <-quotas.C:
			quota()
		case <-probes.C:

			if f.opt.Drive.Offline() {
				_ = f.refreshQuota(ctx)
			}
		}
	}
}

const lastPollSaved = time.Minute

func (f *FS) notePoll() error {
	now := f.clock.Now()
	if now.Sub(f.pollSaved) < lastPollSaved {
		return nil
	}
	if err := f.store.SetLastPoll(now); err != nil {
		return err
	}
	f.pollSaved = now
	return nil
}

func (f *FS) tells(ctx context.Context, err error) bool {
	return err != nil && ctx.Err() == nil && !drive.IsLoginExpired(err)
}
