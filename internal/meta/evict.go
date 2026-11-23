package meta

import "fmt"

type CachedPiece struct {
	Ino Ino
	ID  string
	Off int64
	Len int64
}

func (s *Store) CachedPieces() ([]CachedPiece, error) {
	rows, err := s.r.Query(`SELECT p.ino, i.id, p.off, p.len FROM pieces p JOIN items i ON i.ino = p.ino ` +
		`WHERE i.dirty = 0`)
	if err != nil {
		return nil, fmt.Errorf("meta: cached pieces: %w", err)
	}
	defer rows.Close()
	var out []CachedPiece
	for rows.Next() {
		var p CachedPiece
		if err := rows.Scan(&p.Ino, &p.ID, &p.Off, &p.Len); err != nil {
			return nil, fmt.Errorf("meta: cached pieces: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) DeletePiece(ino Ino, off int64) error {
	return exec(s.w, "delete piece", `DELETE FROM pieces WHERE ino = ? AND off = ?`, ino, off)
}
