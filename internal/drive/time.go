package drive

import (
	"math"
	"strings"
	"time"
)

func earliest() time.Time { return time.Unix(0, math.MinInt64) }

func latest() time.Time { return time.Unix(0, math.MaxInt64) }

func atBound(t time.Time) bool { return t.Equal(earliest()) || t.Equal(latest()) }

func parseTime(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		year, rest, ok := strings.Cut(s, "-")
		if !ok || len(year) <= 4 || strings.Trim(year, "0123456789") != "" {
			return time.Time{}, err
		}

		if _, rerr := time.Parse(time.RFC3339Nano, "2000-"+rest); rerr != nil {
			return time.Time{}, err
		}
		return latest(), nil
	}
	switch {
	case t.Before(earliest()):
		return earliest(), nil
	case t.After(latest()):
		return latest(), nil
	}
	return t, nil
}
