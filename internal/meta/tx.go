package meta

import (
	"database/sql"
	"errors"
	"fmt"
)

func syncOps(tx txn, ino Ino) error {
	it, _, err := scanItem(tx.QueryRow(`SELECT `+itemCols+` FROM items WHERE ino = ?`, ino))
	if errors.Is(err, sql.ErrNoRows) {
		return dropOps(tx, ino)
	}
	if err != nil {
		return err
	}
	if it.metaAsDrive() {
		if _, err := tx.Exec(`UPDATE items SET meta_dirty = 0 WHERE ino = ?`, ino); err != nil {
			return err
		}
		it.MetaDirty = false
	}
	if !opPending(it) {
		return deleteOps(tx, `ino = ?1 AND state != ?2`, ino, OpSending)
	}
	id, err := queueOp(tx, ino)
	if err != nil {
		return err
	}
	return needCreation(tx, id, it.Parent)
}

func opPending(it Item) bool {
	switch it.Standing() {
	case ToCreate, ToSendMeta, GoneInDoubt:

		return true
	case Synced, GoneUnsent, OnlyHere:
		return false
	case ToSendContent:

		return !it.TrashedHere() || it.MetaDirty
	case Broken:

	}
	return true
}

func queueOp(tx txn, ino Ino) (int64, error) {
	var id, earlier int64
	err := tx.QueryRow(`SELECT id FROM ops WHERE ino = ? AND state != ?`, ino, OpSending).Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):

		if err := tx.QueryRow(`SELECT coalesce(max(id), 0) FROM ops WHERE ino = ?`, ino).Scan(&earlier); err != nil {
			return 0, err
		}
		res, err := tx.Exec(`INSERT INTO ops(ino, state) VALUES (?, ?)`, ino, OpQueued)
		if err != nil {
			return 0, err
		}
		if id, err = res.LastInsertId(); err != nil {
			return 0, err
		}
	case err != nil:
		return 0, err
	default:

		if _, err := tx.Exec(`DELETE FROM op_deps WHERE op = ?1 AND needs IN (
				SELECT o.id FROM ops o JOIN items f ON f.ino = o.ino WHERE f.r_exists = 0 AND f.kind = ?2 AND o.ino != ?3)`,
			id, KindDir, ino); err != nil {
			return 0, err
		}
	}
	if earlier != 0 {
		if err := need(tx, id, earlier); err != nil {
			return 0, err
		}
	}
	return id, nil
}

func needCreation(tx txn, id int64, parent string) error {
	var creation int64

	err := tx.QueryRow(`SELECT coalesce(min(o.id), 0) FROM ops o JOIN items p ON p.ino = o.ino WHERE p.id = ? AND `+
		`p.r_exists = 0`,
		parent).Scan(&creation)
	if err != nil || creation == 0 {
		return err
	}
	return need(tx, id, creation)
}

func need(tx txn, op, needs int64) error {
	var loop bool
	err := tx.QueryRow(`WITH RECURSIVE up(id) AS (
			SELECT ?
			UNION SELECT d.needs FROM op_deps d JOIN up ON d.op = up.id
		) SELECT EXISTS (SELECT 1 FROM up WHERE id = ?)`, needs, op).Scan(&loop)
	if err != nil || loop {
		return err
	}
	_, err = tx.Exec(`INSERT OR IGNORE INTO op_deps(op, needs) VALUES (?, ?)`, op, needs)
	return err
}

func dropOps(tx txn, ino Ino) error { return deleteOps(tx, `ino = ?1`, ino) }

func deleteOps(tx txn, where string, args ...any) error {
	for _, q := range []string{
		`DELETE FROM op_deps WHERE op IN (SELECT id FROM ops WHERE ` + where + `) OR needs IN (SELECT id FROM ops ` +
			`WHERE ` + where + `)`,
		`DELETE FROM ops WHERE ` + where,
	} {
		if _, err := tx.Exec(q, args...); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) inTx(what string, change func(tx txn) error) error {
	tx, err := s.w.Begin()
	if err != nil {
		return fmt.Errorf("meta: %s: %w", what, err)
	}
	defer tx.Rollback()
	if err := change(tx); err != nil {
		return fmt.Errorf("meta: %s: %w", what, err)
	}
	if err := commit(tx); err != nil {
		return fmt.Errorf("meta: %s: %w", what, err)
	}
	return nil
}

func commit(tx txn) error {
	if err := nameShown(tx); err != nil {
		return err
	}
	return commitStep(tx)
}

func commitStep(tx txn) error {
	if err := syncTouched(tx); err != nil {
		return err
	}
	return tx.Commit()
}

func syncTouched(tx txn) error {

	for range 4 {
		rows, err := tx.Query(`SELECT t.ino FROM touched t LEFT JOIN items i ON i.ino = t.ino
			ORDER BY coalesce(i.kind != ?, 1), t.ino`, KindDir)
		if err != nil {
			return err
		}
		var inos []Ino
		for rows.Next() {
			var ino Ino
			if err := rows.Scan(&ino); err != nil {
				rows.Close()
				return err
			}
			inos = append(inos, ino)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		if len(inos) == 0 {
			return nil
		}
		if _, err := tx.Exec(`DELETE FROM touched`); err != nil {
			return err
		}
		for _, ino := range inos {
			if err := syncOps(tx, ino); err != nil {
				return err
			}
		}
	}
	return nil
}

func single(tx txn, ino Ino, keep int64) error {
	return deleteOps(tx, `ino = ?1 AND id != ?2`, ino, keep)
}
