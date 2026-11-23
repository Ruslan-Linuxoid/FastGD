package meta

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
)

type unnamed struct {
	ino           Ino
	parent, dname string

	lost bool
}

func unnamedItems(tx txn) (all []unnamed, links []Item, err error) {

	rows, err := tx.Query(`SELECT `+itemCols+` FROM items
		WHERE shown_as = '' AND `+shown+` AND parent != ''
		AND (kind != ?1 OR EXISTS (SELECT 1 FROM items t WHERE t.id = items.target AND t.trashed = 0 AND t.kind != ?1))
		ORDER BY ctime, id`, KindShortcut)
	if err != nil {
		return nil, nil, fmt.Errorf("meta: name the shown: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		it, dname, err := scanItem(rows)
		if err != nil {
			rows.Close()
			return nil, nil, fmt.Errorf("meta: name the shown: %w", err)
		}
		if it.Kind == KindShortcut {
			links = append(links, it)
		}
		all = append(all, unnamed{it.Ino, it.Parent, dname, it.Lost && it.Kind == KindDir && it.Name == LostName})
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, nil, fmt.Errorf("meta: name the shown: %w", err)
	}
	return all, links, nil
}

type namer struct {
	tx txn
	p  *paths

	rest []unnamed
}

func (n *namer) shownOnly(all []unnamed, links []Item, root string) []unnamed {
	for i := range all {
		all[i].lost = all[i].lost && all[i].parent == root
	}
	if len(links) == 0 {
		return all
	}
	hidden := map[Ino]bool{}
	for _, l := range links {
		hidden[l.Ino] = !n.p.linkShown(l)
	}
	return slices.DeleteFunc(all, func(u unnamed) bool { return hidden[u.ino] })
}

func (n *namer) givePlain(all []unnamed) error {
	for _, u := range all {
		if plainAllowed(u.dname) {
			took, err := n.take(u, u.dname)
			if err != nil {
				return err
			}
			if took {
				continue
			}
		}
		n.rest = append(n.rest, u)
	}
	return nil
}

func (n *namer) giveNumbered() error {

	type group struct{ parent, dname string }
	last := map[group]int{}
	for _, u := range n.rest {
		g := group{u.parent, u.dname}
		for k := last[g] + 1; ; k++ {
			took, err := n.take(u, numbered(u.dname, k))
			if err != nil {
				return err
			}
			if took {
				last[g] = k
				break
			}
		}
	}
	return nil
}

func (n *namer) take(u unnamed, name string) (bool, error) {
	holder, held, err := scanItem(n.tx.QueryRow(`SELECT `+itemCols+` FROM items WHERE parent = ? AND shown_as = ?`,
		u.parent, name))
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("meta: name the shown: %w", err)
	}
	if err == nil {
		if freed, err := n.takeFrom(u, holder, held); err != nil || !freed {
			return false, err
		}
	}
	if _, err := n.tx.Exec(`UPDATE items SET shown_as = ? WHERE ino = ?`, name, u.ino); err != nil {
		return false, fmt.Errorf("meta: name the shown: %w", err)
	}
	return true, nil
}

func (n *namer) takeFrom(u unnamed, holder Item, held string) (bool, error) {
	hidden := holder.Kind == KindShortcut && !n.p.linkShown(holder)
	if !hidden && !u.lost {
		return false, nil
	}
	if _, err := n.tx.Exec(`UPDATE items SET shown_as = '' WHERE ino = ?`, holder.Ino); err != nil {
		return false, fmt.Errorf("meta: name the shown: %w", err)
	}
	if !hidden {
		n.rest = append(n.rest, unnamed{ino: holder.Ino, parent: holder.Parent, dname: held})
	}
	return true, nil
}
