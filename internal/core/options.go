package core

import (
	"log/slog"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/clock"
	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

const MaxName = meta.MaxName

type Options struct {
	CacheDir string
	Drive    *drive.Client
	Clock    clock.Clock

	HoleUnit int64

	FieldsMark string

	UID, GID uint32
	Log      *slog.Logger

	CacheSize     int64
	TransferTime  time.Duration
	PrefetchShare float64

	MaxChildren, MaxDepth int
	DailyUpload           int64

	FreeUnits, FreeBytes int64

	MinCacheLimit int64

	CacheFree func() (int64, error)

	MinCacheFree int64

	ShelveFault func(step string) error

	FileMode, DirMode uint32

	ResumableFrom int64

	PollFault func() error

	RecoverFault func(step string, it meta.Item) error
}

func (o *Options) defaults() {
	if o.FieldsMark == "" {
		o.FieldsMark = drive.FieldsMark()
	}
	if o.Clock == nil {
		o.Clock = clock.Real{}
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	o.MaxChildren = positive(o.MaxChildren, drive.MaxChildren)
	o.MaxDepth = positive(o.MaxDepth, drive.MaxDepth)
	o.DailyUpload = positive(o.DailyUpload, drive.DailyUpload)
	o.FreeUnits = positive(o.FreeUnits, drive.FreeUnitsPerDay)
	o.FreeBytes = positive(o.FreeBytes, drive.FreeBytesPerDay)
}

func positive[T int | int64](v, drives T) T {
	if v > 0 {
		return v
	}
	return drives
}
