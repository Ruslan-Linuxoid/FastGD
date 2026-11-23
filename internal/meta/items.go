package meta

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

var ErrIDReused = errors.New("meta: Drive holds an item under the fileId of one made here")

func (s *Store) ResetItems() error {
	return exec(s.w, "reset",
		`DELETE FROM pieces; DELETE FROM upload_sessions; DELETE FROM op_deps; DELETE FROM ops; DELETE FROM items`)
}

func (s *Store) SetRoot(id string, mtime time.Time) error {
	return exec(s.w, "set root", `
		INSERT INTO items(ino, id, kind, mime, name, parent, dname, trashed, size, md5, version, mtime, ctime)
		VALUES (?, ?, ?, ?, '', '', '', 0, 0, '', 0, ?, 0)
		ON CONFLICT(id) DO UPDATE SET mtime = excluded.mtime`,
		RootIno, id, KindDir, mimeFolder, nanos(mtime))
}

const upsertItem = `
	INSERT INTO items(id, kind, mime, name, parent, dname, trashed, size, md5, version, mtime, ctime,
		r_md5, r_version, r_name, r_parent, r_trashed, r_mtime, target, denied, rev)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(id) DO UPDATE SET
		kind = excluded.kind, mime = excluded.mime, ctime = excluded.ctime, target = excluded.target,
		denied = excluded.denied,
		size = CASE WHEN ` + sqlContentLocal + ` THEN size ELSE excluded.size END,
		md5 = CASE WHEN ` + sqlContentLocal + ` THEN md5 ELSE excluded.md5 END,
		rev = CASE WHEN ` + sqlContentLocal + ` THEN rev ELSE excluded.rev END,
		version = CASE WHEN ` + sqlContentLocal + ` THEN version ELSE excluded.version END,
		name = CASE WHEN meta_dirty = 1 THEN name ELSE excluded.name END,
		parent = CASE WHEN meta_dirty = 1 THEN parent ELSE excluded.parent END,
		dname = CASE WHEN meta_dirty = 1 THEN dname ELSE excluded.dname END,
		trashed = CASE WHEN meta_dirty = 1 THEN trashed ELSE excluded.trashed END,
		mtime = CASE WHEN meta_dirty = 1 OR dirty = 1 THEN mtime ELSE excluded.mtime END,
		r_md5 = excluded.r_md5, r_version = excluded.r_version,
		r_name = excluded.r_name, r_parent = excluded.r_parent,
		r_trashed = excluded.r_trashed, r_mtime = excluded.r_mtime`

func upsertArgs(it Item) []any {
	return []any{it.ID, it.Kind, it.Mime, it.Name, it.Parent, it.GroupName(),
		it.Trashed, it.Size, it.MD5, it.Version, nanos(it.Mtime), nanos(it.Ctime),
		it.MD5, it.Version, it.Name, it.Parent, it.Trashed, nanos(it.Mtime), it.ShortcutTarget, it.Denied, it.Rev}
}

func (s *Store) PutItems(items []Item) error {
	tx, err := s.w.Begin()
	if err != nil {
		return fmt.Errorf("meta: put: %w", err)
	}
	defer tx.Rollback()
	st, err := tx.Prepare(upsertItem)
	if err != nil {
		return fmt.Errorf("meta: put: %w", err)
	}
	defer st.Close()
	for _, it := range items {
		if _, err := st.Exec(upsertArgs(it)...); err != nil {
			return fmt.Errorf("meta: put %s: %w", it.ID, err)
		}
	}
	if err := commit(tx); err != nil {
		return fmt.Errorf("meta: put: %w", err)
	}
	return nil
}

type Change struct {
	ID      string
	Removed bool
	Item    Item
}

type Applied struct {
	Before, After *Item
}

func (s Visible) ApplyChanges(changes []Change, token string) ([]Applied, error) {
	out, err := s.applyChanges(changes, token)
	if err != nil {
		return nil, fmt.Errorf("meta: apply changes: %w", err)
	}
	return out, nil
}

func (s Visible) applyChanges(changes []Change, token string) ([]Applied, error) {
	tx, err := s.w.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	out := make([]Applied, 0, len(changes))
	for _, ch := range changes {
		before, err := itemOrNil(tx, ch.ID)
		if err != nil {
			return nil, err
		}
		after, err := applyOne(tx, ch, before)
		if err != nil {
			return nil, err
		}
		out = append(out, Applied{Before: before, After: after})
	}
	if err := putState(tx, StateChangesToken, token); err != nil {
		return nil, err
	}
	if err := commit(tx); err != nil {
		return nil, err
	}
	return out, nil
}

func itemOrNil(q db, id string) (*Item, error) {
	it, err := itemByID(q, id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &it, nil
}

func keepsBefore(ch Change, before *Item) bool {
	if before == nil {
		return false
	}
	if !ch.Removed {
		return ch.Item.Version < before.RVersion
	}
	return before.AheadOfDrive()
}

func applyOne(tx txn, ch Change, before *Item) (*Item, error) {
	if keepsBefore(ch, before) {

		return before, nil
	}
	if ch.Removed {
		if before == nil {
			return nil, nil
		}
		return nil, deleteItem(tx, before.Ino)
	}
	if before != nil && !before.RExists && !before.CreateTried {
		return nil, fmt.Errorf("%w: %s, here %q", ErrIDReused, ch.ID, before.Name)
	}

	if _, err := tx.Exec(upsertItem, upsertArgs(ch.Item)...); err != nil {
		return nil, fmt.Errorf("change of %s: %w", ch.ID, err)
	}
	return itemOrNil(tx, ch.ID)
}

func deleteItem(tx txn, ino Ino) error {
	if _, err := tx.Exec(`DELETE FROM pieces WHERE ino = ?`, ino); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM items WHERE ino = ?`, ino); err != nil {
		return err
	}
	return dropOps(tx, ino)
}

var itemCols = columnNames(false)

type scanner interface{ Scan(dest ...any) error }

func scanItem(sc scanner) (Item, string, error) {
	var it Item
	var dname string
	into := make([]any, len(itemColumns))
	for i, c := range itemColumns {
		switch {
		case c.name == "dname":
			into[i] = &dname
			continue
		case c.at == nil:
			into[i] = new(any)
			continue
		}
		into[i] = c.at(&it)
	}
	err := sc.Scan(into...)
	return it, dname, err
}

func itemByID(q db, id string) (Item, error) {
	it, _, err := scanItem(q.QueryRow(`SELECT `+itemCols+` FROM items WHERE id = ?`, id))
	return it, err
}

func (s *Store) queryItem(query string, args ...any) (Item, error) {
	it, _, err := scanItem(s.r.QueryRow(query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, ErrNotFound
	}
	if err != nil {
		return Item{}, fmt.Errorf("meta: %w", err)
	}
	if fault := s.readFault.Load(); fault != nil {
		if err := (*fault)(it); err != nil {
			return Item{}, fmt.Errorf("meta: %w", err)
		}
	}
	return it, nil
}

func (s *Store) Knows(id string) (bool, error) {
	return scalar[bool](s.r, "item "+id, `SELECT EXISTS (SELECT 1 FROM items WHERE id = ?)`, id)
}

func (s *Store) ByIno(ino Ino) (Item, error) {
	return s.queryItem(`SELECT `+itemCols+` FROM items WHERE ino = ?`, ino)
}

func (s *Store) ByID(id string) (Item, error) {
	return s.queryItem(`SELECT `+itemCols+` FROM items WHERE id = ?`, id)
}
