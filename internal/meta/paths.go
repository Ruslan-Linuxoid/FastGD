package meta

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
)

func (p *paths) shownItems(items []Item) []Item {
	return slices.DeleteFunc(items, func(it Item) bool { return !p.linkShown(it) })
}

func (p *paths) siblings(parent, dname string) ([]Item, error) {
	rows, err := p.q.Query(siblingsNamed, parent, dname)
	if err != nil {
		return nil, fmt.Errorf("meta: siblings: %w", err)
	}
	out, err := scanItems(rows)
	if err != nil {
		return nil, fmt.Errorf("meta: siblings: %w", err)
	}
	return p.shownItems(out), nil
}

func (p *paths) nameOf(it Item) (string, error) {
	var name string
	err := p.q.QueryRow(`SELECT shown_as FROM items WHERE ino = ?`, it.Ino).Scan(&name)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("meta: name of %s: %w", it.ID, err)
	}
	return name, nil
}

func (s *Store) LastPath(id string) (string, error) {
	root, _, err := s.GetState(StateRootID)
	if err != nil {
		return "", err
	}
	var parts []string
	for depth := 0; id != root && id != "" && depth < maxDepthWalked; depth++ {
		it, err := s.ByID(id)
		if errors.Is(err, ErrNotFound) {
			break
		}
		if err != nil {
			return "", err
		}
		parts = append(parts, it.fullName())
		id = it.Parent
	}
	slices.Reverse(parts)
	return strings.Join(parts, "/"), nil
}

func (s *Store) SiblingNames(parent, base string) ([]string, error) {
	sibs, err := s.paths().siblings(parent, base)
	if err != nil {
		return nil, err
	}
	out := make([]string, len(sibs))
	for i, sib := range sibs {
		out[i] = sib.shownAs
	}
	return out, nil
}

var siblingsNamed = `SELECT ` + itemCols + ` FROM items
	WHERE parent = ? AND dname = ? AND shown_as != '' AND ` + shown

func (s *Store) Children(parent string) ([]Entry, error) {

	tx, err := s.r.Begin()
	if err != nil {
		return nil, fmt.Errorf("meta: children: %w", err)
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT `+itemCols+` FROM items
		WHERE parent = ? AND shown_as != '' AND `+shown+`
		ORDER BY shown_as`, parent)
	if err != nil {
		return nil, fmt.Errorf("meta: children: %w", err)
	}
	all, err := scanItems(rows)
	if err != nil {
		return nil, fmt.Errorf("meta: children: %w", err)
	}
	if betweenListingReads != nil {
		betweenListingReads()
	}
	p := pathsOver(tx)
	if slices.ContainsFunc(all, func(it Item) bool { return it.Kind == KindShortcut }) {
		if err := p.learnFolder(parent, all); err != nil {
			return nil, err
		}
	}
	all = p.shownItems(all)
	out := make([]Entry, len(all))
	for i, it := range all {
		out[i] = Entry{Item: it, Display: it.shownAs}
		if it.Kind == KindShortcut {

			out[i].Link, _ = p.link(it)
		}
	}
	return out, nil
}

var betweenListingReads func()

func (s *Store) Lookup(parent, name string) (Item, error) {
	if name == "" {
		return Item{}, ErrNotFound
	}
	p := s.paths()
	rows, err := p.q.Query(`SELECT `+itemCols+` FROM items WHERE parent = ? AND shown_as = ? AND `+shown, parent, name)
	if err != nil {
		return Item{}, fmt.Errorf("meta: lookup: %w", err)
	}
	found, err := scanItems(rows)
	if err != nil {
		return Item{}, fmt.Errorf("meta: lookup: %w", err)
	}
	if found = p.shownItems(found); len(found) > 0 {
		return found[0], nil
	}
	return Item{}, ErrNotFound
}

func scanItems(rows *sql.Rows) ([]Item, error) {
	defer rows.Close()
	var out []Item
	for rows.Next() {
		it, _, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, it)
	}
	return out, rows.Err()
}

func (s *Store) Path(id string) (string, error) { return s.paths().of(id) }

func (s *Store) Link(it Item) (string, error) { return s.paths().link(it) }

func (s *Store) ShownNameOf(it Item) (string, error) { return s.paths().nameOf(it) }
