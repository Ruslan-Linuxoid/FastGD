package meta

import (
	"errors"
	"fmt"
)

func (s *Store) addCreateTried() error {
	tx, err := s.w.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`ALTER TABLE items ADD COLUMN create_tried INTEGER NOT NULL DEFAULT 0`,
		`UPDATE items SET create_tried = 1 WHERE r_exists = 0`,
		fmt.Sprintf(`PRAGMA user_version = %d`, schemaV11),
	} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	return commitStep(tx)
}

func relinkMime(tx txn, mime string) error {

	rows, err := tx.Query(`SELECT ino, name FROM items WHERE mime = ?`, mime)
	if err != nil {
		return err
	}
	var items []Item
	for rows.Next() {
		it := Item{Mime: mime}
		if err := rows.Scan(&it.Ino, &it.Name); err != nil {
			rows.Close()
			return err
		}
		items = append(items, it)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, it := range items {
		if _, err := tx.Exec(`UPDATE items SET kind = ?, dname = ? WHERE ino = ?`, KindDoc, it.GroupName(),
			it.Ino); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM pieces WHERE ino = ?`, it.Ino); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) toLinkFiles() error {
	tx, err := s.w.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for mime := range linkTypes {
		if err := relinkMime(tx, mime); err != nil {
			return err
		}
	}
	if err := putState(tx, StateDropExports, "1"); err != nil {
		return err
	}
	for _, q := range []string{
		`DROP TABLE IF EXISTS exports`,
		fmt.Sprintf(`PRAGMA user_version = %d`, schemaV12),
	} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	return commitStep(tx)
}

func (s *Store) addRev() error {
	tx, err := s.w.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`ALTER TABLE items ADD COLUMN rev TEXT NOT NULL DEFAULT ''`,
		fmt.Sprintf(`PRAGMA user_version = %d`, schemaV13),
	} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	return commitStep(tx)
}

func (s *Store) addLost() error {
	tx, err := s.w.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`ALTER TABLE items ADD COLUMN lost INTEGER NOT NULL DEFAULT 0`,

		`UPDATE items SET md5 = r_md5 WHERE dirty = 1 AND r_exists = 1 AND base = 0`,
		fmt.Sprintf(`PRAGMA user_version = %d`, schemaV14),
	} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	return commitStep(tx)
}

func (s *Store) addService() error {
	tx, err := s.w.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`ALTER TABLE items ADD COLUMN service INTEGER NOT NULL DEFAULT 0`,
		fmt.Sprintf(`PRAGMA user_version = %d`, schemaV17),
	} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	return commitStep(tx)
}

func (s *Store) dropValues() error {
	tx, err := s.w.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	qs := []string{`DROP INDEX IF EXISTS pieces_value`}

	valued, err := hasColumn(tx, "pieces", "hits")
	if err != nil {
		return err
	}
	if valued {
		qs = append(qs, `ALTER TABLE pieces DROP COLUMN hits`, `ALTER TABLE pieces DROP COLUMN value`)
	}
	qs = append(qs,
		`DROP TABLE IF EXISTS evicted`,
		`DELETE FROM state WHERE key = 'evict_floor'`,
		fmt.Sprintf(`PRAGMA user_version = %d`, schemaV16))
	for _, q := range qs {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	return commitStep(tx)
}
