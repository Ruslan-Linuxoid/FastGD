package meta

import (
	"database/sql"
	"errors"
)

func (s Visible) pruneStep(tx txn, id string) (parent string, more bool, err error) {
	dir, ok, err := prunable(tx, id)
	if !ok {
		return "", false, err
	}
	above, named, err := folderAbove(tx, dir.Parent)
	if err != nil {
		return "", false, err
	}
	res, err := tx.Exec(`DELETE FROM items WHERE ino = ? AND `+sqlEmptyUnsent, dir.Ino)
	if err != nil {
		return "", false, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return "", false, nil
	}

	if named && dir.shownAs != "" {
		*s.gone = append(*s.gone, Gone{Parent: above, Name: dir.shownAs})
	}
	if named && dir.Name == LostName {
		if err := s.nameAnew(tx, dir, above); err != nil {
			return "", false, err
		}
	}
	return dir.Parent, true, nil
}

func folderAbove(tx txn, id string) (above Ino, named bool, err error) {
	err = tx.QueryRow(`SELECT ino FROM items WHERE id = ?`, id).Scan(&above)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, false, err
	}
	return above, err == nil, nil
}

func prunable(tx txn, id string) (Item, bool, error) {
	dir, err := itemByID(tx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return dir, false, nil
	}
	if err != nil {
		return dir, false, err
	}
	return dir, dir.Lost && dir.Kind == KindDir, nil
}

func (s Visible) nameAnew(tx txn, dir Item, above Ino) error {
	rows, err := tx.Query(`SELECT shown_as FROM items WHERE parent = ? AND dname = ? AND shown_as != ''`, dir.Parent,
		LostName)
	if err != nil {
		return err
	}
	for rows.Next() {
		var was string
		if err := rows.Scan(&was); err != nil {
			rows.Close()
			return err
		}
		*s.gone = append(*s.gone, Gone{Parent: above, Name: was})
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE items SET shown_as = '' WHERE parent = ? AND dname = ?`, dir.Parent, LostName)
	return err
}
