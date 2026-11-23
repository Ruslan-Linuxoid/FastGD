package core

import (
	"context"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

const quotaInterval = 5 * time.Minute

const probeInterval = 5 * time.Second

const unknownFree = 1 << 50

func (f *FS) refreshQuota(ctx context.Context) error {
	a, err := f.opt.Drive.Account(ctx)
	if err != nil {
		return err
	}
	f.quota.Store(&a.Quota)
	if a.Email != "" && a.Email != f.email() {
		if err := f.store.SetState(meta.StateEmail, a.Email); err != nil {
			return err
		}
	}

	if err := f.store.SetQuota(a.Quota.Limit, a.Quota.Usage, a.Quota.Files()); err != nil {
		return err
	}
	f.cache.SetDriveFiles(a.Quota.Files())
	return nil
}

func (f *FS) loadQuota() {
	limit, usage, files, filesKnown, ok := f.store.Quota()
	if !ok {
		return
	}
	f.quota.Store(&drive.Quota{Limit: limit, Usage: usage})
	if filesKnown {
		f.cache.SetDriveFiles(files)
	}
}

func (f *FS) StatFs() (total, free int64) {
	q := f.quota.Load()
	if q == nil || q.Limit == 0 {
		var used int64
		if q != nil {
			used = q.Usage
		}
		return used + unknownFree, unknownFree
	}
	return q.Limit, max(0, q.Limit-q.Usage)
}
