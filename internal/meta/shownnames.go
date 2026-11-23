package meta

import (
	"errors"
	"fmt"
	"strconv"
)

func nameShown(tx txn) error {
	all, links, err := unnamedItems(tx)
	if err != nil {
		return err
	}
	if len(all) == 0 {
		return nil
	}
	n := &namer{tx: tx, p: pathsOver(tx)}
	root, err := n.p.rootID()
	if err != nil {
		return err
	}
	all = n.shownOnly(all, links, root)
	if err := n.givePlain(all); err != nil {
		return err
	}
	return n.giveNumbered()
}

func (s *Store) keepShownNames() error {
	have, err := hasColumn(s.w, "items", "shown_as")
	if err != nil {
		return err
	}
	tx, err := s.w.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if !have {
		if _, err := tx.Exec(`ALTER TABLE items ADD COLUMN shown_as TEXT NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) nameAll() error {
	tx, err := s.w.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := nameShown(tx); err != nil {
		return err
	}
	return tx.Commit()
}

const nameRules = 4

const stateNameRules = "name_rules"

func (s *Store) renameGroups() error {

	rules, err := stateIn(s.w, stateNameRules)
	if err != nil {
		return err
	}
	if rules == strconv.Itoa(nameRules) {
		return nil
	}
	stale, err := s.staleGroups()
	if err != nil {
		return err
	}

	tx, err := s.w.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, n := range stale {

		kind := n.it.Kind
		if kind == KindHidden {
			kind = KindOf(n.it.Mime)
		}
		if _, err := tx.Exec(`UPDATE items SET dname = ?, kind = ? WHERE ino = ?`, n.it.GroupName(), kind,
			n.it.Ino); err != nil {
			return err
		}
	}
	if err := putState(tx, stateNameRules, strconv.Itoa(nameRules)); err != nil {
		return err
	}
	return tx.Commit()
}

type named struct {
	it    Item
	dname string
}

func (s *Store) staleGroups() ([]named, error) {
	rows, err := s.w.Query(`SELECT ` + itemCols + ` FROM items`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var stale []named
	for rows.Next() {
		it, dname, err := scanItem(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		if dname != it.GroupName() || it.Kind == KindHidden {
			stale = append(stale, named{it, dname})
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	return stale, nil
}
