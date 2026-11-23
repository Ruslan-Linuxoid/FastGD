package meta

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

var ErrNoIDs = errors.New("meta: no fileIds left in the pool")

func (s *Store) AddIDs(ids []string) error {
	return s.inTx("add ids", func(tx txn) error {
		for _, id := range ids {
			if _, err := tx.Exec(`INSERT INTO id_pool(id) VALUES (?) ON CONFLICT DO NOTHING`, id); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) PoolSize() (int, error) {
	return scalar[int](s.r, "pool size", `SELECT count(*) FROM id_pool`)
}

func takeID(tx txn) (string, error) {
	for {
		var id string
		err := tx.QueryRow(`DELETE FROM id_pool WHERE id = (SELECT id FROM id_pool LIMIT 1) RETURNING id`).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return "", ErrNoIDs
		}
		if err != nil {
			return "", err
		}
		var taken bool
		if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM items WHERE id = ?)`, id).Scan(&taken); err != nil {
			return "", err
		}
		if !taken {
			return id, nil
		}
	}
}

func (s *Store) DropIDs(taken func(id string) bool) error {
	rows, err := s.r.Query(`SELECT id FROM id_pool`)
	if err != nil {
		return fmt.Errorf("meta: drop ids: %w", err)
	}
	defer rows.Close()
	var drop []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return fmt.Errorf("meta: drop ids: %w", err)
		}
		if taken(id) {
			drop = append(drop, id)
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return fmt.Errorf("meta: drop ids: %w", err)
	}
	if len(drop) == 0 {
		return nil
	}
	return s.inTx("drop ids", func(tx txn) error {
		for _, id := range drop {
			if _, err := tx.Exec(`DELETE FROM id_pool WHERE id = ?`, id); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) TakeID() (string, error) {
	tx, err := s.w.Begin()
	if err != nil {
		return "", fmt.Errorf("meta: take id: %w", err)
	}
	defer tx.Rollback()
	id, err := takeID(tx)
	if err != nil {
		if errors.Is(err, ErrNoIDs) {
			return "", err
		}
		return "", fmt.Errorf("meta: take id: %w", err)
	}
	return id, commit(tx)
}

const localKeySuffix = ".local"

func NewLocalKey() (string, error) {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]) + localKeySuffix, nil
}

func LocalKeyFor(place string) string {
	sum := sha256.Sum256([]byte(place))
	return hex.EncodeToString(sum[:12]) + localKeySuffix
}

func IsLocalKey(id string) bool { return strings.HasSuffix(id, localKeySuffix) }

func (s Visible) Rebind(it Item, id string) error {
	return s.inTx("give a fileId", func(tx txn) error {

		type named struct {
			ino  Ino
			name string
		}
		var kept []named
		rows, err := tx.Query(`SELECT ino, shown_as FROM items WHERE parent = ? AND shown_as != ''`, it.ID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var n named
			if err := rows.Scan(&n.ino, &n.name); err != nil {
				rows.Close()
				return err
			}
			kept = append(kept, n)
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		for _, q := range []string{
			`UPDATE items SET id = ?1 WHERE id = ?2`,
			`UPDATE items SET parent = ?1 WHERE parent = ?2`,
			`UPDATE upload_sessions SET parent = ?1 WHERE parent = ?2`,
		} {
			if _, err := tx.Exec(q, id, it.ID); err != nil {
				return err
			}
		}
		for _, n := range kept {
			if _, err := tx.Exec(`UPDATE items SET shown_as = ? WHERE ino = ?`, n.name, n.ino); err != nil {
				return err
			}
		}
		if it.Kind != KindFile {
			return nil
		}
		_, err = tx.Exec(`INSERT INTO shelf(took, had) VALUES (?, ?)`, id, it.ID)
		return err
	})
}
