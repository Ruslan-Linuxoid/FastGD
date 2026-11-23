package core

import (
	"sync"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
)

const usageEvery = 10 * time.Second

const usageWarnAt = 0.9

const usageCalmAt = 0.8

type usageCounts struct {
	mu sync.Mutex

	pending drive.Usage

	unitsLevel, bytesLevel int
}

func (f *FS) saveUsage() {
	f.usage.mu.Lock()
	defer f.usage.mu.Unlock()
	u := f.opt.Drive.TakeUsage()
	p := &f.usage.pending
	p.Units += u.Units
	p.Bytes += u.Bytes
	now := f.clock.Now()
	since := now.Add(-drive.QuotaWindow)
	if p.Units > 0 || p.Bytes > 0 {
		if err := f.store.AddUsage(now, p.Units, p.Bytes, since); err != nil {
			f.log.Warn("save the use of the quota", "err", err)
			return
		}
		*p = drive.Usage{}
	}
	units, bytes, err := f.store.UsageSince(since)
	if err != nil {
		f.log.Warn("read the use of the quota", "err", err)
		return
	}
	f.usage.unitsLevel = f.warnUsage("quota units", units, f.opt.FreeUnits, f.usage.unitsLevel)
	f.usage.bytesLevel = f.warnUsage("downloaded bytes", bytes, f.opt.FreeBytes, f.usage.bytesLevel)
}

func usageLevel(n, free int64) int {
	switch {
	case n > free:
		return 2
	case float64(n) >= usageWarnAt*float64(free):
		return 1
	}
	return 0
}

func usageLevelAfter(n, free int64, was int) int {
	level := usageLevel(n, free)
	if level == 0 && was == 1 && float64(n) >= usageCalmAt*float64(free) {
		return 1
	}
	return level
}

func (f *FS) warnUsage(what string, n, free int64, was int) int {
	level := usageLevelAfter(n, free, was)
	switch {
	case level <= was:
	case level == 1:
		f.log.Warn("the count of the last 24 hours nears the project's free threshold of Google",
			"what", what, "count", n, "threshold", free)
	default:
		f.log.Warn("the count of the last 24 hours passed the project's free threshold of Google; Google plans to bill "+
			"past it",
			"what", what, "count", n, "threshold", free)
	}
	return level
}
