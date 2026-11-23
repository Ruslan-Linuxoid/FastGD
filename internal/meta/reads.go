package meta

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const opCols = `id, ino, state, attempts, next_try, last_error, refused, refused_for, busy, busy_for`

func scanOp(sc scanner) (Op, error) {
	var op Op
	var next int64
	err := sc.Scan(&op.ID, &op.Ino, &op.State, &op.Attempts, &next, &op.LastError, &op.Refused, &op.refusedFor, &op.Busy,
		&op.busyFor)
	op.NextTry = fromNanos(next)
	return op, err
}

func scanOps(rows *sql.Rows) ([]Op, error) {
	defer rows.Close()
	var out []Op
	for rows.Next() {
		op, err := scanOp(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, rows.Err()
}

const arrivingOp = `SELECT coalesce(max(o.id), 0) FROM ops o JOIN items i ON i.ino = o.ino
	WHERE o.ino = ? AND (i.dirty = 1 OR i.r_exists = 0)`

func (s *Store) ReadyOps(now time.Time, limit int, skip []Ino) ([]Op, error) {
	q := `SELECT ` + prefixed("o.",
		opCols) + ` FROM ops o JOIN items c ON c.ino = o.ino LEFT JOIN items p ON p.id = c.parent
		WHERE o.state != ? AND o.next_try <= ? AND c.upload_after <= ?
			AND NOT EXISTS (SELECT 1 FROM op_deps d JOIN ops n ON n.id = d.needs WHERE d.op = o.id)
			AND (p.r_exists = 1 OR c.trashed = 1)`
	args := []any{OpSending, nanos(now), nanos(now)}
	if len(skip) > 0 {
		q += ` AND o.ino NOT IN (?` + strings.Repeat(", ?", len(skip)-1) + `)`
		for _, ino := range skip {
			args = append(args, ino)
		}
	}
	rows, err := s.r.Query(q+` ORDER BY o.next_try, o.id LIMIT ?`, append(args, limit)...)
	if err != nil {
		return nil, fmt.Errorf("meta: ready operations: %w", err)
	}
	out, err := scanOps(rows)
	if err != nil {
		return nil, fmt.Errorf("meta: ready operations: %w", err)
	}
	return out, nil
}

func (s *Store) Ops(ino Ino) ([]Op, error) {
	rows, err := s.r.Query(`SELECT `+opCols+` FROM ops WHERE ino = ? ORDER BY id`, ino)
	if err != nil {
		return nil, fmt.Errorf("meta: operations: %w", err)
	}
	out, err := scanOps(rows)
	if err != nil {
		return nil, fmt.Errorf("meta: operations: %w", err)
	}
	return out, nil
}

func (s *Store) Successors(id int64) ([]Op, error) {
	rows, err := s.r.Query(`WITH RECURSIVE down(id) AS (
			SELECT op FROM op_deps WHERE needs = ?
			UNION SELECT d.op FROM op_deps d JOIN down ON d.needs = down.id
		) SELECT `+opCols+` FROM ops WHERE id IN (SELECT id FROM down) ORDER BY id`, id)
	if err != nil {
		return nil, fmt.Errorf("meta: successors: %w", err)
	}
	out, err := scanOps(rows)
	if err != nil {
		return nil, fmt.Errorf("meta: successors: %w", err)
	}
	return out, nil
}

func (s *Store) CountOps() (int, error) {
	var n int
	if err := s.r.QueryRow(`SELECT count(*) FROM ops`).Scan(&n); err != nil {
		return 0, fmt.Errorf("meta: operations: %w", err)
	}
	return n, nil
}

func (s *Store) HasOps() (bool, error) {
	var left bool
	if err := s.r.QueryRow(`SELECT EXISTS (SELECT 1 FROM ops)`).Scan(&left); err != nil {
		return false, fmt.Errorf("meta: operations: %w", err)
	}
	return left, nil
}

func prefixed(prefix, cols string) string {
	parts := strings.Split(cols, ",")
	for i, c := range parts {
		parts[i] = prefix + strings.TrimSpace(c)
	}
	return strings.Join(parts, ", ")
}
