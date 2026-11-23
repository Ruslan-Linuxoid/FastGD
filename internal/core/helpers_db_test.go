package core_test

import (
	"context"
	"database/sql"
)

func asBlockSchema(path string) error {
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		return err
	}
	defer db.Close()
	for _, q := range []string{
		`DROP TRIGGER IF EXISTS items_changed`, `DROP TRIGGER IF EXISTS items_made`, `DROP TRIGGER IF EXISTS items_gone`,
		`DROP TABLE pieces`,
		`ALTER TABLE items DROP COLUMN denied`,
		`ALTER TABLE items DROP COLUMN base`,
		`ALTER TABLE items DROP COLUMN base_tail`,
		`ALTER TABLE items DROP COLUMN create_tried`,
		`ALTER TABLE items DROP COLUMN rev`,
		`ALTER TABLE items DROP COLUMN lost`,
		`ALTER TABLE items DROP COLUMN service`,
		`CREATE TABLE blocks (ino INTEGER NOT NULL, idx INTEGER NOT NULL, PRIMARY KEY (ino, idx)) WITHOUT ROWID`,
		`INSERT INTO blocks SELECT ino, 0 FROM items WHERE dirty = 0 AND kind = 0`,
		`PRAGMA user_version = 6`,
	} {
		if _, err := db.ExecContext(context.Background(), q); err != nil {
			return err
		}
	}
	return nil
}

func execSQL(path, q string) error {
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		return err
	}
	defer db.Close()
	for _, trigger := range []string{"items_changed", "items_made", "items_gone", "items_renamed"} {
		if _, err := db.ExecContext(context.Background(), `DROP TRIGGER IF EXISTS `+trigger); err != nil {
			return err
		}
	}
	_, err = db.ExecContext(context.Background(), q)
	return err
}

func execSQLScan(path, q string, dst ...any) error {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return err
	}
	defer db.Close()
	return db.QueryRowContext(context.Background(), q).Scan(dst...)
}
