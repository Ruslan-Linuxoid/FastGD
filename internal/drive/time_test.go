package drive

import (
	"math"
	"testing"
	"time"
)

func TestTimesOfDriveAreBoundedToTheStore(t *testing.T) {
	t.Parallel()
	earliest, latest := time.Unix(0, math.MinInt64), time.Unix(0, math.MaxInt64)
	for s, want := range map[string]time.Time{
		"60056-05-28T05:36:10.000Z": latest,
		"9999-12-31T23:59:59.000Z":  latest,
		"0001-01-01T00:00:00.000Z":  earliest,
		"60000-02-29T00:00:00.000Z": latest,
		"2026-11-22T16:52:44.450Z":  time.Date(2026, 11, 22, 16, 52, 44, 450e6, time.UTC),
		"":                          {},
	} {
		if got, err := parseTime(s); err != nil || !got.Equal(want) {
			t.Errorf("%q reads as %v, %v, want %v", s, got, err, want)
		}
	}
	for _, s := range []string{"not a time", "60056x05-28", "-60056-05-28T05:36:10Z", "60056-garbage",
		"123456-99-99T99:99:99Z"} {
		if got, err := parseTime(s); err == nil {
			t.Errorf("%q reads as %v, want an error", s, got)
		}
	}
}
