package meta

import (
	"fmt"
	"time"
)

func (s *Store) SetLocal(ino Ino, size int64, mtime time.Time, gen int64, uploadAfter time.Time) error {
	return exec(s.w, "set local state",
		`UPDATE items SET size = ?, mtime = ?, content_gen = ?, upload_after = ? WHERE ino = ?`,
		size, nanos(mtime), gen, nanos(uploadAfter), ino)
}

func (s *Store) DirtyIDs() ([]string, error) {
	rows, err := s.r.Query(dirtyIDs)
	if err != nil {
		return nil, fmt.Errorf("meta: dirty: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("meta: dirty: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

const dirtyIDs = `SELECT id FROM items WHERE dirty = 1`

func (s *Store) Dirty() ([]Item, error) {
	rows, err := s.r.Query(`SELECT ` + itemCols + ` FROM items WHERE dirty = 1`)
	if err != nil {
		return nil, fmt.Errorf("meta: dirty: %w", err)
	}
	out, err := scanItems(rows)
	if err != nil {
		return nil, fmt.Errorf("meta: dirty: %w", err)
	}
	return out, nil
}

func (s *Store) DirtyBytes() (int64, error) {
	return scalar[int64](s.r, "dirty bytes",
		`SELECT coalesce(sum(size), 0) FROM items WHERE dirty = 1 AND trashed = 0 AND `+sqlGoesToDrive)
}

func (s *Store) MarkDirty(ino Ino, base int64, tail bool) error {
	return s.inTx("mark dirty", func(tx txn) error {
		_, err := tx.Exec(`UPDATE items SET dirty = 1, base = ?, base_tail = ? WHERE ino = ?`, base, tail, ino)
		return err
	})
}

func (s *Store) SetBase(ino Ino, base int64, tail bool) error {
	return exec(s.w, "set base", `UPDATE items SET base = ?, base_tail = ? WHERE ino = ?`, base, tail, ino)
}

func (s *Store) SetRev(ino Ino, rev string) error {
	return exec(s.w, "set revision", `UPDATE items SET rev = ? WHERE ino = ?`, rev, ino)
}

func (s *Store) SetMtime(ino Ino, mtime time.Time) error {
	return s.inTx("set mtime", func(tx txn) error {
		_, err := tx.Exec(`UPDATE items SET mtime = ?, meta_dirty = 1 WHERE ino = ?`, nanos(mtime), ino)
		return err
	})
}
