package meta

import (
	"database/sql"
	"errors"
	"fmt"
)

func (s Visible) Create(it Item) (Item, error) {
	tx, err := s.w.Begin()
	if err != nil {
		return Item{}, fmt.Errorf("meta: create: %w", err)
	}
	defer tx.Rollback()
	id, err := NewLocalKey()
	if err != nil {
		return Item{}, fmt.Errorf("meta: create: %w", err)
	}
	it.ID = id
	it.RExists = false
	it.Dirty = it.Kind == KindFile
	after := nanos(it.UploadAfter)
	_, err = tx.Exec(`INSERT INTO items(id, kind, mime, name, parent, dname, trashed, size, md5, version, mtime, ctime, `+
		`r_exists, dirty, upload_after, service)
		VALUES (?, ?, ?, ?, ?, ?, 0, ?, '', 0, ?, ?, 0, ?, ?, ?)`,
		it.ID, it.Kind, it.Mime, it.Name, it.Parent, it.GroupName(), it.Size,
		nanos(it.Mtime), nanos(it.Ctime), it.Dirty, after, it.Service)
	if err != nil {
		return Item{}, fmt.Errorf("meta: create %s: %w", it.Name, err)
	}
	created, err := itemByID(tx, it.ID)
	if err != nil {
		return Item{}, fmt.Errorf("meta: create: %w", err)
	}
	if err := commit(tx); err != nil {
		return Item{}, fmt.Errorf("meta: create: %w", err)
	}
	return created, nil
}

func (s Visible) Rename(it Item, parent, name string, service *bool) error {
	return s.inTx("rename", func(tx txn) error {
		err := s.place(tx, it, parent, name)
		if err != nil || service == nil {
			return err
		}
		return setService(tx, it.ID, *service)
	})
}

func (s Visible) place(tx txn, it Item, parent, name string) error {
	had := it
	it.Name = name
	_, err := tx.Exec(`UPDATE items SET parent = ?, name = ?, dname = ?, meta_dirty = 1 WHERE ino = ?`,
		parent, name, it.GroupName(), it.Ino)
	if err != nil {
		return err
	}
	return s.leaveLost(tx, had, parent)
}

func (s Visible) Trash(ino Ino) error {
	return s.inTx("trash", func(tx txn) error { return trash(tx, ino) })
}

func trash(tx txn, ino Ino) error {
	_, err := tx.Exec(`UPDATE items SET trashed = 1, meta_dirty = 1 WHERE ino = ?`, ino)
	return err
}

func (s *Store) Unsent() ([]Item, error) {
	rows, err := s.r.Query(`SELECT ` + itemCols + ` FROM items WHERE ` + sqlGoneUnsent)
	if err != nil {
		return nil, fmt.Errorf("meta: unsent: %w", err)
	}
	return scanItems(rows)
}

func (s Visible) Forget(ino Ino) (bool, error) {
	tx, err := s.w.Begin()
	if err != nil {
		return false, fmt.Errorf("meta: forget: %w", err)
	}
	defer tx.Rollback()
	var parent string
	err = tx.QueryRow(`DELETE FROM items WHERE ino = ? AND r_exists = 0 RETURNING parent`, ino).Scan(&parent)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("meta: forget: %w", err)
	}
	if err := s.dropNotes(tx, ino); err != nil {
		return false, fmt.Errorf("meta: forget: %w", err)
	}
	if err := s.pruneLost(tx, parent); err != nil {
		return false, fmt.Errorf("meta: forget: %w", err)
	}

	if _, err := tx.Exec(`DELETE FROM pieces WHERE ino = ?`, ino); err != nil {
		return false, fmt.Errorf("meta: forget: %w", err)
	}
	if err := dropOps(tx, ino); err != nil {
		return false, fmt.Errorf("meta: forget: %w", err)
	}
	return true, commit(tx)
}

func (s Visible) Replace(it Item, target Ino, parent, name string, service *bool) error {
	tx, err := s.w.Begin()
	if err != nil {
		return fmt.Errorf("meta: replace: %w", err)
	}
	defer tx.Rollback()

	waiters, err := waitingFor(tx, target)
	if err != nil {
		return fmt.Errorf("meta: replace: %w", err)
	}
	if err := trash(tx, target); err != nil {
		return fmt.Errorf("meta: replace: %w", err)
	}
	if err := s.place(tx, it, parent, name); err != nil {
		return fmt.Errorf("meta: replace: %w", err)
	}
	if service != nil {
		if err := setService(tx, it.ID, *service); err != nil {
			return fmt.Errorf("meta: replace: %w", err)
		}
	}

	if err := syncTouched(tx); err != nil {
		return fmt.Errorf("meta: replace: %w", err)
	}
	if err := waitForReplacement(tx, target, it.Ino, waiters); err != nil {
		return fmt.Errorf("meta: replace: %w", err)
	}
	return commit(tx)
}

func (s Visible) SetDenied(denied map[string]Denied) ([]Ino, error) {
	tx, err := s.w.Begin()
	if err != nil {
		return nil, fmt.Errorf("meta: set rights: %w", err)
	}
	defer tx.Rollback()
	var changed []Ino
	for id, d := range denied {
		var ino Ino
		err := tx.QueryRow(`UPDATE items SET denied = ?1 WHERE id = ?2 AND denied != ?1 RETURNING ino`, d, id).Scan(&ino)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("meta: set rights: %w", err)
		}
		changed = append(changed, ino)
	}
	return changed, commit(tx)
}

func (s *Store) Unuploaded() ([]Item, error) {
	rows, err := s.r.Query(`SELECT `+itemCols+` FROM items
		WHERE `+sqlContentLocal+` AND `+sqlNotLost+` AND trashed = 0 AND kind IN (?, ?)`, KindFile, KindDir)
	if err != nil {
		return nil, fmt.Errorf("meta: unuploaded: %w", err)
	}
	out, err := scanItems(rows)
	if err != nil {
		return nil, fmt.Errorf("meta: unuploaded: %w", err)
	}
	return out, nil
}
