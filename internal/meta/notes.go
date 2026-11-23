package meta

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type Note struct {
	ID, Name string
	Size     int64
}

func addNote(tx txn, of Item, n Note, now time.Time) (Item, error) {
	it := Item{Name: n.Name, Mime: "text/plain"}
	_, err := tx.Exec(`INSERT INTO items(id, kind, mime, name, parent, dname, trashed, size, md5, version, mtime, ctime,`+
		` r_exists, dirty, lost, note_for)
		VALUES (?, ?, ?, ?, ?, ?, 0, ?, '', 0, ?, ?, 0, 1, 1, ?)`,
		n.ID, KindFile, it.Mime, it.Name, of.Parent, it.GroupName(), n.Size, nanos(now), nanos(now), of.Ino)
	if err != nil {
		return Item{}, err
	}
	it, err = itemByID(tx, n.ID)
	return it, err
}

func (s Visible) dropNotes(tx txn, inos ...Ino) error {
	for _, ino := range inos {
		rows, err := tx.Query(`UPDATE items SET trashed = 1 WHERE note_for = ? AND `+sqlLost+` AND trashed = 0 RETURNING `+
			`parent, shown_as`, ino)
		if err != nil {
			return err
		}
		type entry struct{ parent, name string }
		var went []entry
		for rows.Next() {
			var e entry
			if err := rows.Scan(&e.parent, &e.name); err != nil {
				rows.Close()
				return err
			}
			went = append(went, e)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		for _, e := range went {
			var above Ino
			if e.name == "" {

				continue
			}
			if err := tx.QueryRow(`SELECT ino FROM items WHERE id = ?`, e.parent).Scan(&above); err == nil {
				*s.gone = append(*s.gone, Gone{Parent: above, Name: e.name})
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
	}
	return nil
}

func (s *Store) TrashedNotes() ([]Ino, error) {
	rows, err := s.r.Query(`SELECT ino FROM items WHERE note_for != 0 AND trashed = 1`)
	if err != nil {
		return nil, fmt.Errorf("meta: trashed notes: %w", err)
	}
	defer rows.Close()
	var out []Ino
	for rows.Next() {
		var ino Ino
		if err := rows.Scan(&ino); err != nil {
			return nil, fmt.Errorf("meta: trashed notes: %w", err)
		}
		out = append(out, ino)
	}
	return out, rows.Err()
}
