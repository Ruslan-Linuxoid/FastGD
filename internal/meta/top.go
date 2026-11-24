package meta

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

const (
	TopID          = ":top"
	SharedWithMeID = ":shared-with-me"
	SharedDrivesID = ":shared-drives"
)

const (
	MyDriveName      = "My Drive"
	SharedWithMeName = "Shared with me"
	SharedDrivesName = "Shared drives"
)

func IsVirtual(id string) bool { return strings.HasPrefix(id, ":") }

func Fixed(it Item) bool { return IsVirtual(it.ID) || IsVirtual(it.Parent) && !it.Lost }

func FixedIn(parent Item) bool { return IsVirtual(parent.ID) }

func (s *Store) SetTop(rootID string, mtime time.Time) error {
	return s.inTx("set the root of the mount", func(tx txn) error { return setTop(tx, rootID, nanos(mtime)) })
}

func setTop(tx txn, rootID string, at int64) error {
	if _, err := tx.Exec(`
		INSERT INTO items(ino, id, kind, mime, name, parent, dname, trashed, size, md5, version, mtime, ctime, service)
		VALUES (?, ?, ?, ?, '', '', '', 0, 0, '', 0, ?, 0, 1)
		ON CONFLICT(id) DO UPDATE SET mtime = excluded.mtime`,
		RootIno, TopID, KindDir, mimeFolder, at); err != nil {
		return err
	}
	if _, err := tx.Exec(`
		INSERT INTO items(id, kind, mime, name, parent, dname, shown_as, trashed, size, md5, version, mtime, ctime,
			r_name, r_parent)
		VALUES (?, ?, ?, ?, ?, ?, ?, 0, 0, '', 0, ?, 0, ?, ?)
		ON CONFLICT(id) DO UPDATE SET mtime = excluded.mtime, name = excluded.name, parent = excluded.parent,
			dname = excluded.dname, shown_as = excluded.shown_as, r_name = excluded.r_name,
			r_parent = excluded.r_parent`,
		rootID, KindDir, mimeFolder, MyDriveName, TopID, MyDriveName, MyDriveName, at, MyDriveName, TopID); err != nil {
		return err
	}
	folders := []struct{ id, name string }{{SharedWithMeID, SharedWithMeName}, {SharedDrivesID, SharedDrivesName}}
	for _, f := range folders {
		if _, err := tx.Exec(`
			INSERT INTO items(id, kind, mime, name, parent, dname, shown_as, trashed, size, md5, version, mtime,
				ctime, service)
			VALUES (?, ?, ?, ?, ?, ?, ?, 0, 0, '', 0, ?, 0, 1)
			ON CONFLICT(id) DO UPDATE SET mtime = excluded.mtime`,
			f.id, KindDir, mimeFolder, f.name, TopID, f.name, f.name, at); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) addTop() error {
	return s.inTx("put My Drive under the root of the mount", func(tx txn) error {
		var root string
		err := tx.QueryRow(`SELECT value FROM state WHERE key = ?`, StateRootID).Scan(&root)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}

		var done bool
		if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM items WHERE id = ?)`, TopID).Scan(&done); err != nil {
			return err
		}
		if done {
			return nil
		}
		var next Ino
		if err := tx.QueryRow(`SELECT coalesce(max(ino), 0) + 1 FROM items`).Scan(&next); err != nil {
			return err
		}
		if err := moveIno(tx, RootIno, next); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE items SET parent = ? WHERE parent = ? AND lost = 1`, TopID, root); err != nil {
			return err
		}
		return setTop(tx, root, 0)
	})
}

func (s *Store) SectionOf(id string) (string, error) {
	return scalar[string](s.r, "section of "+id, `WITH RECURSIVE up(id, parent, n) AS (
			SELECT id, parent, 0 FROM items WHERE id = ?
			UNION ALL SELECT items.id, items.parent, up.n + 1 FROM items JOIN up ON items.id = up.parent
				WHERE up.parent NOT LIKE ':%' AND up.parent != '' AND up.n < ?
		) SELECT CASE WHEN parent = ? THEN parent ELSE id END FROM up ORDER BY n DESC LIMIT 1`,
		id, maxDepthWalked, SharedWithMeID)
}

func moveIno(tx txn, from, to Ino) error {
	for _, table := range []string{"items", "pieces", "ops", "touched", "upload_sessions"} {
		var have bool
		if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = ?)`,
			table).Scan(&have); err != nil {
			return err
		}
		if !have {
			continue
		}
		if _, err := tx.Exec(`UPDATE `+table+` SET ino = ? WHERE ino = ?`, to, from); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) addShared() error {
	have, err := hasColumn(s.w, "items", "shared")
	if err != nil || have {
		return err
	}
	_, err = s.w.Exec(`ALTER TABLE items ADD COLUMN shared INTEGER NOT NULL DEFAULT 0`)
	return err
}
