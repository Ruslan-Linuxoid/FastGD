package cache

import (
	"context"
	"log/slog"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/clock"
)

type Options struct {
	Dir string

	Records Records
	Source  Source

	Passing func(error) bool
	Log     *slog.Logger

	HoleUnit int64

	punchHoleAt func(fd int, off, n int64) error

	MinFree int64

	Size  int64
	Share float64

	TransferTime time.Duration

	Free func() (int64, error)

	MinLimit int64

	Clock clock.Clock

	MaxSpeed int64

	LimitChanged func(limit int64, by string) error

	Mixed func(ctx context.Context, f File)
}
