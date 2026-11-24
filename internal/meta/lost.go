package meta

import (
	"database/sql"
	"errors"
	"time"
)

const LostName = "Lost and Found (FastGD)"

func (s Visible) ForgetEmpty(ino Ino) (bool, error) {
	var gone bool
	err := s.inTx("forget an empty folder", func(tx txn) error {
		res, err := tx.Exec(`DELETE FROM items WHERE ino = ? AND kind = ? AND `+sqlEmptyUnsent, ino, KindDir)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		gone = n > 0
		return nil
	})
	return gone, err
}

var ErrLostOnly = errors.New("meta: Lost and Found holds only what the daemon put there")

func MayCreateIn(parent Item) error {
	if parent.Lost {
		return ErrLostOnly
	}
	return nil
}

func MayMove(src, srcDir, dst Item) error {
	intoLost := dst.Lost && !src.Lost
	lostItself := src.Lost && !srcDir.Lost
	if intoLost || lostItself {
		return ErrLostOnly
	}
	return nil
}

func lostFolder(tx txn, dirs []string, now time.Time) (string, []Item, error) {
	parent := TopID
	var made []Item
	for _, dir := range append([]string{LostName}, dirs...) {
		var have string
		err := tx.QueryRow(`SELECT id FROM items WHERE parent = ? AND name = ? AND `+sqlLost+` AND kind = ? AND trashed = 0`,
			parent, dir, KindDir).Scan(&have)
		if errors.Is(err, sql.ErrNoRows) {
			if have, err = NewLocalKey(); err != nil {
				return "", nil, err
			}
			d := Item{Name: dir, Mime: mimeFolder}
			_, err = tx.Exec(`INSERT INTO items(id, kind, mime, name, parent, dname, trashed, size, md5, version, mtime, `+
				`ctime, r_exists, lost)
				VALUES (?, ?, ?, ?, ?, ?, 0, 0, '', 0, ?, ?, 0, 1)`,
				have, KindDir, d.Mime, d.Name, parent, d.GroupName(), nanos(now), nanos(now))
			if err != nil {
				return "", nil, err
			}
			it, err := itemByID(tx, have)
			if err != nil {
				return "", nil, err
			}
			made = append(made, it)
		} else if err != nil {
			return "", nil, err
		}
		parent = have
	}
	return parent, made, nil
}

func (s Visible) AddLost(
	id string, dirs []string, name, mime string, size int64, mtime time.Time, note *Note, now time.Time,
) error {
	return s.inTx("add to Lost and Found", func(tx txn) error {
		parent, _, err := lostFolder(tx, dirs, now)
		if err != nil {
			return err
		}
		it := Item{Name: name, Mime: mime}
		_, err = tx.Exec(`INSERT INTO items(id, kind, mime, name, parent, dname, trashed, size, md5, version, mtime, ctime,`+
			` r_exists, dirty, lost)
			VALUES (?, ?, ?, ?, ?, ?, 0, ?, '', 0, ?, ?, 0, 1, 1)`,
			id, KindFile, it.Mime, it.Name, parent, it.GroupName(), size, nanos(mtime), nanos(now))
		if err != nil || note == nil {
			return err
		}

		lost, err := itemByID(tx, id)
		if err != nil {
			return err
		}
		_, err = addNote(tx, lost, *note, now)
		return err
	})
}

func (s Visible) leaveLost(tx txn, had Item, parent string) error {
	if !had.Lost {
		return nil
	}
	var within bool
	err := tx.QueryRow(`SELECT lost FROM items WHERE id = ?`, parent).Scan(&within)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if within {
		return nil
	}
	inos, err := subtree(tx, had.ID)
	if err != nil {
		return err
	}

	if err := s.dropNotes(tx, inos...); err != nil {
		return err
	}
	for _, ino := range inos {
		if _, err := tx.Exec(`UPDATE items SET lost = 0 WHERE ino = ?`, ino); err != nil {
			return err
		}
	}
	return s.pruneLost(tx, had.Parent)
}

func (s Visible) pruneLost(tx txn, id string) error {
	for {
		next, more, err := s.pruneStep(tx, id)
		if err != nil || !more {
			return err
		}
		id = next
	}
}

func (s Visible) TakeRemote(ino Ino, remote *Item) error {
	return s.inTx("take Drive's state", func(tx txn) error {

		row := `UPDATE items SET dirty = 0, meta_dirty = 0, base = 0, base_tail = 0 WHERE ino = ?`
		if remote == nil {
			row = `DELETE FROM items WHERE ino = ?`
		}
		for _, q := range []string{`DELETE FROM pieces WHERE ino = ?`, row} {
			if _, err := tx.Exec(q, ino); err != nil {
				return err
			}
		}
		if remote != nil {
			if _, err := tx.Exec(upsertItem, upsertArgs(*remote)...); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s Visible) TakeRemoteName(it Item, name, parent string) error {
	it.Name = name
	return s.inTx("take Drive's name", func(tx txn) error {
		_, err := tx.Exec(`UPDATE items SET name = ?, parent = ?, dname = ?, r_name = ?, r_parent = ?,
			meta_dirty = (trashed != r_trashed OR mtime != r_mtime) WHERE ino = ?`,
			name, parent, it.GroupName(), name, parent, it.Ino)
		return err
	})
}
