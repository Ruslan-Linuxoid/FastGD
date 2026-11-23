package meta

import (
	"errors"
	"fmt"
	"path/filepath"
)

type paths struct {
	q db

	root   string
	rooted bool

	items map[string]*Item

	checked map[string]error

	at map[string]string
}

func (s *Store) paths() *paths { return pathsOver(s.r) }

func pathsOver(q db) *paths {
	return &paths{q: q, items: map[string]*Item{}, checked: map[string]error{}, at: map[string]string{}}
}

func chains(seed string) string {
	return `WITH RECURSIVE up(id) AS (` + seed + `
		UNION SELECT i.parent FROM items i JOIN up ON i.id = up.id WHERE i.parent != '')
	SELECT ` + itemCols + ` FROM items t WHERE t.id IN (SELECT id FROM up)`
}

var (
	chainOfOne = chains(`SELECT ?`)

	chainsOfFolder = chains(`SELECT ?1 UNION SELECT target FROM items WHERE parent = ?1 AND kind = ?2`)
)

func (p *paths) learn(query string, args ...any) error {
	rows, err := p.q.Query(query, args...)
	if err != nil {
		return fmt.Errorf("meta: paths: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		it, _, err := scanItem(rows)
		if err != nil {
			return fmt.Errorf("meta: paths: %w", err)
		}
		p.items[it.ID] = &it
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("meta: paths: %w", err)
	}
	return nil
}

func (p *paths) learnFolder(parent string, children []Item) error {
	if err := p.learn(chainsOfFolder, parent, KindShortcut); err != nil {
		return err
	}
	for _, it := range children {
		if _, ok := p.items[it.ShortcutTarget]; it.Kind == KindShortcut && !ok {
			p.items[it.ShortcutTarget] = nil
		}
	}
	return nil
}

func (p *paths) item(id string) (*Item, error) {
	if _, ok := p.items[id]; !ok {
		p.items[id] = nil
		if err := p.learn(chainOfOne, id); err != nil {
			delete(p.items, id)
			return nil, err
		}
	}
	if pl := p.items[id]; pl != nil {
		return pl, nil
	}
	return nil, ErrNotFound
}

func (p *paths) rootID() (string, error) {
	if !p.rooted {
		root, err := stateIn(p.q, StateRootID)
		if err != nil {
			return "", fmt.Errorf("meta: state %s: %w", StateRootID, err)
		}
		p.root, p.rooted = root, true
	}
	return p.root, nil
}

func (p *paths) visible(id string) error {
	root, err := p.rootID()
	if err != nil || id == root {
		return err
	}
	if err, ok := p.checked[id]; ok {
		return err
	}

	p.checked[id] = fmt.Errorf("meta: parent loop at %s", id)
	err = p.climb(id)
	p.checked[id] = err
	return err
}

func (p *paths) climb(id string) error {
	it, err := p.item(id)
	if err != nil {
		return err
	}
	if it.Trashed || it.Parent == "" || !isListed(it.Kind) || !p.linkShown(*it) {
		return ErrNotShown
	}
	if err := p.visible(it.Parent); !errors.Is(err, ErrNotFound) {
		return err
	}
	return ErrNotShown
}

const maxDepthWalked = 10000

func (p *paths) linkShown(it Item) bool {
	if it.Kind != KindShortcut {
		return true
	}
	target, err := p.item(it.ShortcutTarget)
	if err != nil || target.Kind == KindShortcut || p.visible(target.ID) != nil {
		return false
	}

	for id, depth := it.Parent, 0; id != "" && depth < maxDepthWalked; depth++ {
		if id == target.ID {
			return false
		}
		above, err := p.item(id)
		if err != nil {
			break
		}
		id = above.Parent
	}
	return true
}

func (p *paths) of(id string) (string, error) {
	if err := p.visible(id); err != nil || id == p.root {
		return "", err
	}
	if at, ok := p.at[id]; ok {
		return at, nil
	}
	it := p.items[id]
	at := it.shownAs
	if at == "" {
		return "", ErrNotShown
	}
	dir, err := p.of(it.Parent)
	if err != nil {
		return "", err
	}
	if dir != "" {
		at = dir + "/" + at
	}
	p.at[id] = at
	return at, nil
}

func (p *paths) link(it Item) (string, error) {
	dir, err := p.of(it.Parent)
	if err != nil {
		return "", err
	}
	target, err := p.of(it.ShortcutTarget)
	if err != nil {
		return "", err
	}
	return filepath.Rel("/"+dir, "/"+target)
}
