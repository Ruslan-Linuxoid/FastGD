package core

import (
	"strconv"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

const limitWarnEvery = 10 * time.Minute

func (f *FS) cacheLimitChanged(limit int64, by string) error {
	err := f.store.SetState(meta.StateCacheLimit, strconv.FormatInt(limit, 10)+" "+by)
	if err == nil {
		f.limitWarnedAt.Store(0)
		return nil
	}
	now := f.clock.Now().UnixNano()
	if last := f.limitWarnedAt.Load(); last == 0 || time.Duration(now-last) >= limitWarnEvery {
		f.log.Warn("write the cache limit for status", "err", err)
		f.limitWarnedAt.Store(now)
	}
	return err
}
