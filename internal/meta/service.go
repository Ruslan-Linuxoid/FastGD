package meta

import (
	"errors"
	"fmt"
	"regexp"
)

var serviceNames = regexp.MustCompile(`^(?:\.~lock\..*#|.*\.swp|.*\.crdownload|(?i:desktop\.ini|thumbs\.db|` +
	`EFS\d+\.tmp|.*~RF[0-9a-fA-F]+\.TMP|\.DS_Store|Icon\r))$`)

func isServiceName(name string) bool { return serviceNames.MatchString(name) }

func setService(tx txn, id string, on bool) error {
	inos, err := subtree(tx, id)
	if err != nil {
		return err
	}
	for _, ino := range inos {
		if _, err := tx.Exec(`UPDATE items SET service = ? WHERE ino = ?`, on, ino); err != nil {
			return err
		}
	}
	return nil
}

var ErrHoldsDriveItems = errors.New("meta: an item of Drive cannot become a service item")

func ServiceIn(parent Item, name string) bool { return parent.Service || isServiceName(name) }

func (s *Store) ServiceAfterMove(src, dst Item, name string) (*bool, error) {
	switch {
	case src.Service && !dst.Service && !isServiceName(name):
		return new(bool), nil
	case !src.Service && dst.Service:
		held, err := s.HoldsDriveItems(src.ID)
		if err != nil {
			return nil, err
		}
		if held {
			return nil, ErrHoldsDriveItems
		}
		on := true
		return &on, nil
	}
	return nil, nil
}

func (s *Store) HoldsDriveItems(id string) (bool, error) {
	var held bool
	err := s.r.QueryRow(`WITH RECURSIVE down(id) AS (
			SELECT ?
			UNION SELECT items.id FROM items JOIN down ON items.parent = down.id
		) SELECT EXISTS (SELECT 1 FROM items JOIN down ON items.id = down.id WHERE r_exists = 1 OR create_tried = 1)`,
		id).Scan(&held)
	if err != nil {
		return false, fmt.Errorf("meta: items of Drive under %s: %w", id, err)
	}
	return held, nil
}

func subtree(tx txn, id string) ([]Ino, error) {
	rows, err := tx.Query(`WITH RECURSIVE down(id, depth) AS (
			SELECT ?, 0
			UNION SELECT items.id, down.depth + 1 FROM items JOIN down ON items.parent = down.id
		) SELECT items.ino FROM items JOIN down ON items.id = down.id ORDER BY down.depth`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var inos []Ino
	for rows.Next() {
		var ino Ino
		if err := rows.Scan(&ino); err != nil {
			return nil, err
		}
		inos = append(inos, ino)
	}
	return inos, rows.Err()
}
