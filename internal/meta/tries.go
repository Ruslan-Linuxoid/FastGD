package meta

import (
	"database/sql"
	"errors"
	"time"
)

func (s *Store) NoteTry(op Op, attempts int, next time.Time, msg string) error {
	return exec(s.w, "note a failed try",
		`UPDATE ops SET attempts = ?, next_try = ?, last_error = ?, noted = 1 WHERE id = ? AND state = ?`,
		attempts, nanos(next), msg, op.ID, OpSending)
}

func (s *Store) TakeBackTry(op Op) error {
	return exec(s.w, "note a failed try",
		`UPDATE ops SET attempts = attempts - noted, next_try = 0, last_error = '', noted = 0
		WHERE id = ? AND state = ? AND noted = 1`, op.ID, OpSending)
}

func (s *Store) FailOp(op Op, attempts int, next time.Time, msg string, why Failure) error {
	net, refused, busy := why == FailedOffline, why == FailedRefused, why == FailedBusy
	return s.inTx("fail an operation", func(tx txn) error {
		if err := single(tx, op.Ino, op.ID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE ops SET state = ? WHERE id = ?`, OpFailed, op.ID); err != nil {
			return err
		}

		if err := syncOps(tx, op.Ino); err != nil {
			return err
		}

		it, _, err := scanItem(tx.QueryRow(`SELECT `+itemCols+` FROM items WHERE ino = ?`, op.Ino))
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		_, err = tx.Exec(`UPDATE ops SET attempts = ?1, next_try = ?2, last_error = ?3, noted = 0, net = ?7,
			refused = CASE WHEN NOT ?4 THEN 0 WHEN refused_for = ?5 THEN refused + 1 ELSE 1 END,
			refused_for = CASE WHEN ?4 THEN ?5 ELSE '' END,
			busy = CASE WHEN busy_for != ?5 THEN ?8 ELSE busy + ?8 END,
			busy_for = CASE WHEN ?8 OR busy_for = ?5 THEN ?5 ELSE '' END WHERE id = ?6`,
			attempts, nanos(next), msg, refused, asked(it), op.ID, net, busy)
		return err
	})
}
