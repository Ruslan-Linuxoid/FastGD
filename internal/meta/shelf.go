package meta

import (
	"fmt"
	"time"
)

type Shelf struct{ s *Store }

func (s *Store) Shelf() Shelf { return Shelf{s} }

type Shelved struct {
	Lost Item

	Dirs []Item

	Note Item
}

func (s Visible) Shelve(
	it Item, id string, dirs []string, name string, remote *Item, note *Note, now time.Time,
) (Shelved, error) {
	var out Shelved
	fail := func(err error) (Shelved, error) {
		return Shelved{}, fmt.Errorf("meta: shelve %s: %w", it.ID, err)
	}
	tx, err := s.w.Begin()
	if err != nil {
		return fail(err)
	}
	defer tx.Rollback()
	parent, made, err := lostFolder(tx, dirs, now)
	if err != nil {
		return fail(err)
	}
	out.Dirs = made
	if id == "" {
		id = it.ID
	} else if _, err := tx.Exec(`INSERT INTO shelf(took, had) VALUES (?, ?)`, id, it.ID); err != nil {
		return fail(err)
	}
	if err := moveRow(tx, it, id, parent, name); err != nil {
		return fail(err)
	}
	if out.Lost, err = itemByID(tx, id); err != nil {
		return fail(err)
	}
	if out.Note, err = storeNotes(tx, out.Lost, note, remote, now); err != nil {
		return fail(err)
	}
	if err := commit(tx); err != nil {
		return fail(err)
	}
	return out, nil
}

func (sh Shelf) Unfinished() (map[string]string, error) {
	rows, err := sh.s.r.Query(`SELECT took, had FROM shelf`)
	if err != nil {
		return nil, fmt.Errorf("meta: journal of moves: %w", err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var took, had string
		if err := rows.Scan(&took, &had); err != nil {
			return nil, fmt.Errorf("meta: journal of moves: %w", err)
		}
		out[took] = had
	}
	return out, rows.Err()
}

func (sh Shelf) Finished(id string) error {
	if _, err := sh.s.w.Exec(`DELETE FROM shelf WHERE took = ?`, id); err != nil {
		return fmt.Errorf("meta: journal of moves: %w", err)
	}
	return nil
}

func moveRow(tx txn, it Item, id, parent, name string) error {
	moved := Item{Name: name, Mime: it.Mime}
	_, err := tx.Exec(`UPDATE items SET id = ?, lost = 1, parent = ?, name = ?, dname = ?, trashed = 0, dirty = 1,
		r_exists = 0, meta_dirty = 0, md5 = '', rev = '', version = 0, r_md5 = '', r_version = 0, r_name = '', r_parent = '',
		r_trashed = 0, r_mtime = 0, base = 0, base_tail = 0, create_tried = 0, denied = 0, upload_after = 0 WHERE ino = ?`,
		id, parent, name, moved.GroupName(), it.Ino)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`DELETE FROM upload_sessions WHERE ino = ?`, it.Ino)
	return err
}

func storeNotes(tx txn, lost Item, note *Note, remote *Item, now time.Time) (Item, error) {
	var stored Item
	if note != nil {
		var err error
		if stored, err = addNote(tx, lost, *note, now); err != nil {
			return Item{}, err
		}
	}
	if remote != nil {
		if _, err := tx.Exec(upsertItem, upsertArgs(*remote)...); err != nil {
			return Item{}, err
		}
	}
	return stored, nil
}
