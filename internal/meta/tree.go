package meta

import (
	"fmt"
)

func (s *Store) HasChildren(id string) (bool, error) {
	return scalar[bool](s.r, "children of "+id, `SELECT EXISTS (SELECT 1 FROM items WHERE parent = ? AND trashed = 0)`, id)
}

func (s *Store) ChildCount(id string, limit int) (int, error) {
	return scalar[int](s.r, "children of "+id, `SELECT count(*) FROM (SELECT 1 FROM items WHERE parent = ? LIMIT ?)`, id,
		limit)
}

func (s *Store) Depth(id string, limit int) (int, error) {
	return scalar[int](s.r, "depth of "+id, `WITH RECURSIVE up(id, n) AS (
			SELECT ?, 0
			UNION ALL SELECT items.parent, up.n + 1 FROM items JOIN up ON items.id = up.id
				WHERE items.parent != '' AND items.parent NOT LIKE ':%' AND up.n <= ?
		) SELECT max(n) FROM up`, id, limit)
}

func (s *Store) Height(id string, limit int) (int, error) {
	return scalar[int](s.r, "height of "+id, heightOf, id, limit)
}

func (s *Store) InDrive() ([]string, error) {
	rows, err := s.r.Query(`SELECT id FROM items WHERE r_exists = 1 AND trashed = 0 AND service = 0`)
	if err != nil {
		return nil, fmt.Errorf("meta: items in Drive: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("meta: items in Drive: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

const heightOf = `WITH RECURSIVE down(id, n) AS (
		SELECT ?, 0
		UNION ALL SELECT items.id, down.n + 1 FROM items JOIN down ON items.parent = down.id
			WHERE items.kind = 1 AND down.n <= ?
	) SELECT max(n) FROM down`

func (s *Store) Shortcuts() ([]Item, error) {

	rows, err := s.r.Query(`SELECT `+itemCols+` FROM items WHERE target != '' AND kind = ?`, KindShortcut)
	if err != nil {
		return nil, fmt.Errorf("meta: shortcuts: %w", err)
	}
	out, err := scanItems(rows)
	if err != nil {
		return nil, fmt.Errorf("meta: shortcuts: %w", err)
	}
	return out, nil
}

var shortcutsTo = `SELECT ` + itemCols + ` FROM items WHERE target = ? AND target != '' AND kind = ?`

func (s *Store) ShortcutsTo(id string) ([]Item, error) {
	rows, err := s.r.Query(shortcutsTo, id, KindShortcut)
	if err != nil {
		return nil, fmt.Errorf("meta: shortcuts to %s: %w", id, err)
	}
	out, err := scanItems(rows)
	if err != nil {
		return nil, fmt.Errorf("meta: shortcuts to %s: %w", id, err)
	}
	return out, nil
}
