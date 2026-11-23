package meta

import (
	"fmt"
	"strings"
)

const swappedPrefix = "swapped:"

var driveCols = columnNames(true)

func (s Visible) Swap(it, target Item, parent, name string) error {
	tx, err := s.w.Begin()
	if err != nil {
		return fmt.Errorf("meta: swap: %w", err)
	}
	defer tx.Rollback()
	if err := swap(tx, it, target, parent, name); err != nil {
		return fmt.Errorf("meta: swap %s into %s: %w", it.ID, target.ID, err)
	}
	if it.Lost {

		if err := s.pruneLost(tx, it.Parent); err != nil {
			return fmt.Errorf("meta: swap %s into %s: %w", it.ID, target.ID, err)
		}
	}
	return commit(tx)
}

func swap(tx txn, it, target Item, parent, name string) error {

	waiters, err := waitingFor(tx, target.Ino)
	if err != nil {
		return err
	}
	if err := dropOps(tx, target.Ino); err != nil {
		return err
	}
	placed := Item{Name: name, Mime: target.Mime}
	type stmt struct {
		q    string
		args []any
	}
	stmts := []stmt{
		{`DELETE FROM pieces WHERE ino = ?`, []any{target.Ino}},
		{`DELETE FROM upload_sessions WHERE ino IN (?, ?)`, []any{target.Ino, it.Ino}},

		{`UPDATE items SET id = 'swap:' || id WHERE ino = ?`, []any{target.Ino}},
		{`UPDATE items SET (id, ` + driveCols + `) = (SELECT ?, ` + driveCols + ` FROM items WHERE ino = ?),
			name = ?, parent = ?, dname = ?, trashed = 0, dirty = 1, meta_dirty = 1, base = 0, base_tail = 0,
			lost = 0, service = 0 WHERE ino = ?`,
			[]any{target.ID, target.Ino, name, parent, placed.GroupName(), it.Ino}},
	}
	if !it.NeverTried() {
		if !it.RExists {

			it.RName, it.RParent, it.RMtime = it.Name, it.Parent, it.Mtime
		}
		had := Item{Name: it.RName, Mime: it.Mime}

		held := driveValues(it)
		stmts = append(stmts, stmt{`UPDATE items SET (id, ` + driveCols + `) = (?` + strings.Repeat(", ?", len(held)) + `),
			name = ?, parent = ?, dname = ?, mtime = ?, trashed = 1, meta_dirty = 1, dirty = 0, base = 0, base_tail = 0 ` +
			`WHERE ino = ?`,
			append(append([]any{it.ID}, held...), it.RName, it.RParent, had.GroupName(), nanos(it.RMtime), target.Ino)})
	} else {
		stmts = append(stmts, stmt{`DELETE FROM items WHERE ino = ?`, []any{target.Ino}})
	}
	for _, st := range stmts {
		if _, err := tx.Exec(st.q, st.args...); err != nil {
			return err
		}
	}
	if err := putState(tx, swappedPrefix+target.ID, it.ID); err != nil {
		return err
	}

	if err := syncTouched(tx); err != nil {
		return err
	}

	return waitForReplacement(tx, target.Ino, it.Ino, waiters)
}

func waitForReplacement(tx txn, target, by Ino, waiters []int64) error {
	var trashing, arriving int64
	if err := tx.QueryRow(`SELECT coalesce(max(id), 0) FROM ops WHERE ino = ? AND state != ?`, target,
		OpSending).Scan(&trashing); err != nil {
		return err
	}
	if err := tx.QueryRow(arrivingOp, by).Scan(&arriving); err != nil {
		return err
	}
	if arriving == 0 {
		return nil
	}
	if trashing != 0 {
		waiters = append(waiters, trashing)
	}
	for _, w := range waiters {
		if err := need(tx, w, arriving); err != nil {
			return err
		}
	}
	return nil
}

func waitingFor(tx txn, ino Ino) ([]int64, error) {
	rows, err := tx.Query(`SELECT DISTINCT d.op FROM op_deps d JOIN ops n ON n.id = d.needs JOIN ops o ON o.id = d.op
		WHERE n.ino = ?1 AND o.ino != ?1`, ino)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *Store) Unswapped() (map[string]string, error) { return s.statesUnder(swappedPrefix) }

func (s *Store) Swapped(id string) error { return s.DeleteState(swappedPrefix + id) }
