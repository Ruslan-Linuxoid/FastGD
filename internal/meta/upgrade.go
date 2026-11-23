package meta

import (
	"fmt"
)

func hasColumn(q db, table, col string) (bool, error) {
	var n int
	err := q.QueryRow(`SELECT count(*) FROM pragma_table_info(?) WHERE name = ?`, table, col).Scan(&n)
	return n > 0, err
}

func (s *Store) fromBlocks() error {
	tx, err := s.w.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DROP TABLE IF EXISTS blocks`); err != nil {
		return err
	}
	if err := putState(tx, StateDropClean, "1"); err != nil {
		return err
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaV7)); err != nil {
		return err
	}
	return commitStep(tx)
}

func (s *Store) plainOps() error {
	tx, err := s.w.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	told, err := hasColumn(tx, "ops", "kind")
	if err != nil {
		return err
	}
	qs := []string{fmt.Sprintf(`PRAGMA user_version = %d`, schemaV19)}
	if told {
		qs = append([]string{
			`ALTER TABLE ops DROP COLUMN kind`, `ALTER TABLE ops DROP COLUMN before`, `ALTER TABLE ops DROP COLUMN after`,
			`ALTER TABLE ops DROP COLUMN source`, `ALTER TABLE ops ADD COLUMN refused_for TEXT NOT NULL DEFAULT ''`,
			`UPDATE ops SET refused = 0`,
		}, qs...)
	}
	for _, q := range qs {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	return commitStep(tx)
}

func (s *Store) ownShelf() error {
	tx, err := s.w.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`CREATE TABLE IF NOT EXISTS shelf (took TEXT PRIMARY KEY, had TEXT NOT NULL) WITHOUT ROWID`,
		`INSERT INTO shelf(took, had) SELECT substr(key, 9), value FROM state WHERE key LIKE 'shelved:%'`,
		`DELETE FROM state WHERE key LIKE 'shelved:%'`,
		fmt.Sprintf(`PRAGMA user_version = %d`, schemaV22),
	} {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) keepNotes() error {
	have, err := hasColumn(s.w, "items", "note_for")
	if err != nil {
		return err
	}
	tx, err := s.w.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if !have {
		if _, err := tx.Exec(`ALTER TABLE items ADD COLUMN note_for INTEGER NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaV24)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) countBusy() error {
	have, err := hasColumn(s.w, "ops", "busy")
	if err != nil {
		return err
	}
	tx, err := s.w.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	qs := []string{fmt.Sprintf(`PRAGMA user_version = %d`, schemaV23)}
	if !have {
		qs = append([]string{
			`ALTER TABLE ops ADD COLUMN busy INTEGER NOT NULL DEFAULT 0`,
			`ALTER TABLE ops ADD COLUMN busy_for TEXT NOT NULL DEFAULT ''`,
		}, qs...)
	}
	for _, q := range qs {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) oneCopyOfTries() error {
	tx, err := s.w.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	qs := []string{`DROP INDEX IF EXISTS items_pending`}
	for _, col := range []string{"attempts", "next_try", "last_error"} {
		ok, err := hasColumn(tx, "items", col)
		if err != nil {
			return err
		}
		if ok {
			qs = append(qs, `ALTER TABLE items DROP COLUMN `+col)
		}
	}
	netQs, err := markNetOps(tx)
	if err != nil {
		return err
	}
	qs = append(qs, netQs...)
	qs = append(qs, `DROP TABLE IF EXISTS net_failures`, fmt.Sprintf(`PRAGMA user_version = %d`, schemaV20))
	for _, q := range qs {
		if _, err := tx.Exec(q); err != nil {
			return err
		}
	}
	return commitStep(tx)
}

func markNetOps(tx txn) ([]string, error) {
	ops, err := hasColumn(tx, "ops", "id")
	if err != nil {
		return nil, err
	}
	marked, err := hasColumn(tx, "ops", "net")
	if err != nil {
		return nil, err
	}
	if !ops || marked {
		return nil, nil
	}
	qs := []string{`ALTER TABLE ops ADD COLUMN net INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE ops ADD COLUMN noted INTEGER NOT NULL DEFAULT 0`}
	failed, err := hasColumn(tx, "net_failures", "ino")
	if err != nil {
		return nil, err
	}
	if failed {
		qs = append(qs, `UPDATE ops SET net = 1 WHERE ino IN (SELECT ino FROM net_failures)`)
	}
	return qs, nil
}

func (s *Store) addRights() error {
	tx, err := s.w.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`ALTER TABLE items ADD COLUMN denied INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := putState(tx, StateRightsUnknown, "1"); err != nil {
		return err
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaV8)); err != nil {
		return err
	}
	return commitStep(tx)
}

func (s *Store) addBase() error {
	tx, err := s.w.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`ALTER TABLE items ADD COLUMN base INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if _, err := tx.Exec(`ALTER TABLE items ADD COLUMN base_tail INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaV9)); err != nil {
		return err
	}
	return commitStep(tx)
}

func (s *Store) addValues() error {
	tx, err := s.w.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var pieces bool
	if err := tx.QueryRow(`SELECT count(*) > 0 FROM sqlite_master ` +
		`WHERE type = 'table' AND name = 'pieces'`).Scan(&pieces); err != nil {
		return err
	}
	if pieces {
		if _, err := tx.Exec(`ALTER TABLE pieces ADD COLUMN hits INTEGER NOT NULL DEFAULT 1`); err != nil {
			return err
		}
		if _, err := tx.Exec(`ALTER TABLE pieces ADD COLUMN value REAL NOT NULL DEFAULT 0`); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaV10)); err != nil {
		return err
	}
	return commitStep(tx)
}
