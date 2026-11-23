package drive

import "time"

const (
	MaxChildren = 500_000

	MaxDepth = 100

	DailyUpload = 750_000_000_000
)

const QuotaWindow = 24 * time.Hour

const (
	FreeUnitsPerDay = 400_000_000

	FreeBytesPerDay = 1_000_000_000_000
)

type Usage struct {
	Units int64
	Bytes int64
}

func (c *Client) TakeUsage() Usage {
	return Usage{Units: c.units.Swap(0), Bytes: c.bytes.Swap(0)}
}
