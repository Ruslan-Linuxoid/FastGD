package core

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
)

type budget struct {
	quota      atomic.Pointer[drive.Quota]
	quotaStale atomic.Bool

	refused atomic.Pointer[string]

	lastReadOnlyReason atomic.Pointer[string]

	upMu  sync.Mutex
	upSum int64
	upAt  time.Time

	usage usageCounts

	dirtyOverWarnedAt time.Time

	limitWarnedAt atomic.Int64

	dlRefused atomic.Pointer[string]

	abRefused atomic.Pointer[string]

	dlMu sync.Mutex
}
