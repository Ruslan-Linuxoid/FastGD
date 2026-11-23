package meta

import (
	"fmt"
	"time"
)

func (s *Store) RecordUpload(at time.Time, bytes int64, since time.Time) error {
	return s.inTx("record upload", func(tx txn) error {
		if _, err := tx.Exec(`DELETE FROM uploaded WHERE at < ?`, nanos(since)); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE uploaded SET at = ? WHERE at > ?`, nanos(at), nanos(at)); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO uploaded(at, bytes) VALUES (?, ?)`, nanos(at), bytes); err != nil {
			return err
		}
		return nil
	})
}

func (s *Store) UploadedSince(since time.Time) (int64, error) {
	return scalar[int64](s.r, "uploaded since", `SELECT coalesce(sum(bytes), 0) FROM uploaded WHERE at >= ?`, nanos(since))
}

func (s *Store) AddUsage(at time.Time, units, bytes int64, since time.Time) error {
	return s.inTx("add usage", func(tx txn) error {
		if _, err := tx.Exec(`DELETE FROM usage WHERE minute < ?`, minuteOf(since)); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO usage(minute, units, bytes) VALUES (?, ?, ?)
			ON CONFLICT(minute) DO UPDATE SET units = units + excluded.units, bytes = bytes + excluded.bytes`,
			minuteOf(at), units, bytes)
		if err != nil {
			return err
		}
		return nil
	})
}

func (s *Store) UsageSince(since time.Time) (units, bytes int64, err error) {
	return usageSince(s.r, since)
}

func usageSince(q db, since time.Time) (units, bytes int64, err error) {
	err = q.QueryRow(`SELECT coalesce(sum(units), 0), coalesce(sum(bytes), 0) FROM usage WHERE minute >= ?`,
		minuteOf(since)).
		Scan(&units, &bytes)
	if err != nil {
		return 0, 0, fmt.Errorf("meta: usage since: %w", err)
	}
	return units, bytes, nil
}

func minuteOf(t time.Time) int64 { return t.Unix() / int64(time.Minute/time.Second) }
