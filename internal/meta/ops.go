package meta

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

type OpState int

const (
	OpQueued OpState = iota

	OpSending

	OpFailed
)

type Op struct {
	ID    int64
	Ino   Ino
	State OpState

	Attempts  int
	NextTry   time.Time
	LastError string

	Refused    int
	refusedFor string

	Busy    int
	busyFor string
}

func (op Op) BusyWith(it Item) int {
	if op.busyFor != asked(it) {
		return 1
	}
	return op.Busy + 1
}

type Failure int

const (
	Failed Failure = iota

	FailedOffline

	FailedRefused

	FailedBusy
)

func (op Op) RefusedWith(it Item) int {
	if op.refusedFor != asked(it) {
		return 1
	}
	return op.Refused + 1
}

func asked(it Item) string {
	return fmt.Sprintf("%s\x00%s\x00%t\x00%d\x00%d", it.Name, it.Parent, it.Trashed, nanos(it.Mtime), it.Gen)
}

func (s *Store) SeedOps() error {
	tx, err := s.w.Begin()
	if err != nil {
		return fmt.Errorf("meta: seed operations: %w", err)
	}
	defer tx.Rollback()

	rows, err := tx.Query(`SELECT ino FROM items
		WHERE (`+sqlContentLocal+` OR meta_dirty = 1) AND `+sqlGoesToDrive+` AND ino NOT IN (SELECT ino FROM ops)
		ORDER BY kind != ?, ino`, KindDir)
	if err != nil {
		return fmt.Errorf("meta: seed operations: %w", err)
	}
	defer rows.Close()
	var inos []Ino
	for rows.Next() {
		var ino Ino
		if err := rows.Scan(&ino); err != nil {
			rows.Close()
			return fmt.Errorf("meta: seed operations: %w", err)
		}
		inos = append(inos, ino)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return fmt.Errorf("meta: seed operations: %w", err)
	}
	for _, ino := range inos {
		if err := syncOps(tx, ino); err != nil {
			return fmt.Errorf("meta: seed operations: %w", err)
		}
	}
	return commit(tx)
}

func (s *Store) StartOp(id int64) error {
	return exec(s.w, "start an operation", `UPDATE ops SET state = ? WHERE id = ?`, OpSending, id)
}

func (s *Store) DoneOp(op Op) error {
	return s.inTx("finish an operation", func(tx txn) error {
		if err := deleteOps(tx, `id = ?1`, op.ID); err != nil {
			return err
		}

		return syncOps(tx, op.Ino)
	})
}

func (s *Store) RequeueSending(sending ...Ino) error {
	rows, err := s.r.Query(`SELECT `+opCols+` FROM ops WHERE state = ?`, OpSending)
	if err != nil {
		return fmt.Errorf("meta: requeue operations: %w", err)
	}
	ops, err := scanOps(rows)
	if err != nil {
		return fmt.Errorf("meta: requeue operations: %w", err)
	}
	for _, op := range ops {
		if slices.Contains(sending, op.Ino) {
			continue
		}
		if err := s.RequeueOp(op); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) RequeueOp(op Op) error {
	return s.inTx("requeue an operation", func(tx txn) error {
		if err := single(tx, op.Ino, op.ID); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE ops SET state = ?, next_try = CASE WHEN noted = 1 THEN 0 ELSE next_try END, noted = 0
			WHERE id = ? AND state = ?`, OpQueued, op.ID, OpSending)
		if err != nil {
			return err
		}

		return syncOps(tx, op.Ino)
	})
}

func (s Visible) RevertMeta(it Item) error {
	it.Name = it.RName
	return s.inTx("revert metadata", func(tx txn) error {
		_, err := tx.Exec(`UPDATE items SET name = r_name, parent = r_parent, dname = ?, trashed = r_trashed,
			mtime = CASE WHEN dirty = 1 THEN mtime ELSE r_mtime END, meta_dirty = 0 WHERE ino = ?`, it.GroupName(), it.Ino)
		return err
	})
}

func (s *Store) ForgetFutureWaits(now time.Time, longest time.Duration) error {
	limit := nanos(now.Add(longest))

	var late bool
	err := s.r.QueryRow(`SELECT EXISTS (SELECT 1 FROM ops o JOIN items c ON c.ino = o.ino
		WHERE o.state != ? AND o.next_try > ? OR c.upload_after > ?)`, OpSending, limit, limit).Scan(&late)
	if err != nil || !late {
		if err != nil {
			err = fmt.Errorf("meta: forget future waits: %w", err)
		}
		return err
	}
	if _, err := s.w.Exec(`UPDATE ops SET next_try = ? WHERE state != ? AND next_try > ?`, nanos(now), OpSending,
		limit); err != nil {
		return fmt.Errorf("meta: forget future waits: %w", err)
	}
	_, err = s.w.Exec(`UPDATE items SET upload_after = ? WHERE upload_after > ? AND ino IN (SELECT ino FROM ops)`,
		nanos(now), limit)
	if err != nil {
		return fmt.Errorf("meta: forget future waits: %w", err)
	}
	return nil
}

func (s *Store) Hurry() error {
	return exec(s.w, "hurry", `UPDATE ops SET next_try = 0
		WHERE state != ? AND next_try != 0 AND busy = 0 AND refused = 0`, OpSending)
}

func (s *Store) SkipDelays() error {
	return exec(s.w, "skip delays",
		`UPDATE items SET upload_after = 0 WHERE upload_after != 0 AND ino IN (SELECT ino FROM ops)`)
}
