package meta

import (
	"errors"
	"fmt"
	"slices"
	"sync"
)

type sharedList struct {
	mu      sync.Mutex
	changes int64
	made    bool
	entries []Item
}

func (s *Store) sharedItems(tx txn) ([]Item, error) {
	changes, err := sharedChanges(tx)
	if err != nil {
		return nil, err
	}
	s.shared.mu.Lock()
	defer s.shared.mu.Unlock()
	if s.shared.made && changes >= 0 && s.shared.changes == changes {
		return s.shared.entries, nil
	}
	rows, err := tx.Query(`SELECT `+itemCols+` FROM items WHERE `+shown+`
		AND (parent = ? AND shown_as != '' OR shared = 1 AND parent != ?) ORDER BY ctime, id`,
		SharedWithMeID, SharedWithMeID)
	if err != nil {
		return nil, fmt.Errorf("meta: shared with me: %w", err)
	}
	all, err := scanItems(rows)
	if err != nil {
		return nil, fmt.Errorf("meta: shared with me: %w", err)
	}
	entries, err := secondNames(pathsOver(tx), all)
	if err != nil {
		return nil, err
	}
	s.shared.changes, s.shared.made, s.shared.entries = changes, true, entries
	return entries, nil
}

func secondNames(p *paths, all []Item) ([]Item, error) {
	var out, second []Item
	taken := map[string]bool{}
	all, err := p.shownItems(all)
	if err != nil {
		return nil, err
	}
	for _, it := range all {
		if it.Parent == SharedWithMeID {
			out = append(out, it)
			taken[it.shownAs] = true
			continue
		}
		shown, err := p.shownElsewhere(it)
		if err != nil {
			return nil, err
		}
		if shown {
			second = append(second, it)
		}
	}
	out = append(out, giveSecondNames(second, taken)...)
	slices.SortFunc(out, func(a, b Item) int {
		switch {
		case a.shownAs < b.shownAs:
			return -1
		case a.shownAs > b.shownAs:
			return 1
		}
		return 0
	})
	return out, nil
}

func (p *paths) shownElsewhere(it Item) (bool, error) {
	err := p.visible(it.ID)
	switch {
	case err == nil:
		return true, nil
	case !errors.Is(err, ErrNotShown):
		return false, err
	}
	_, err = p.item(it.Parent)
	if errors.Is(err, ErrNotFound) {
		return true, nil
	}
	return false, err
}

func (s *Store) sharedChildren() ([]Entry, error) {
	tx, err := s.r.Begin()
	if err != nil {
		return nil, fmt.Errorf("meta: shared with me: %w", err)
	}
	defer tx.Rollback()
	items, err := s.sharedItems(tx)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(`SELECT `+itemCols+` FROM items WHERE shared = 1 OR parent = ?`, SharedWithMeID)
	if err != nil {
		return nil, fmt.Errorf("meta: shared with me: %w", err)
	}
	now, err := scanItems(rows)
	if err != nil {
		return nil, fmt.Errorf("meta: shared with me: %w", err)
	}
	byID := make(map[string]Item, len(now))
	for _, it := range now {
		byID[it.ID] = it
	}
	fresh := make([]Item, 0, len(items))
	for _, it := range items {
		if row, ok := byID[it.ID]; ok {
			row.shownAs = it.shownAs
			fresh = append(fresh, row)
		}
	}
	return pathsOver(tx).entries(fresh), nil
}

func (s *Store) sharedLookup(name string) (Item, error) {
	tx, err := s.r.Begin()
	if err != nil {
		return Item{}, fmt.Errorf("meta: shared with me: %w", err)
	}
	defer tx.Rollback()
	items, err := s.sharedItems(tx)
	if err != nil {
		return Item{}, err
	}
	for _, it := range items {
		if it.shownAs != name {
			continue
		}
		row, _, err := scanItem(tx.QueryRow(`SELECT `+itemCols+` FROM items WHERE id = ?`, it.ID))
		if err != nil {
			return Item{}, fmt.Errorf("meta: shared with me: %w", err)
		}
		row.shownAs = it.shownAs
		return row, nil
	}
	return Item{}, ErrNotFound
}

func giveSecondNames(second []Item, taken map[string]bool) []Item {
	for i, it := range second {
		name := it.GroupName()
		for n := 1; !plainAllowed(name) || taken[name]; n++ {
			name = numbered(it.GroupName(), n)
		}
		taken[name] = true
		second[i].shownAs = name
	}
	return second
}
