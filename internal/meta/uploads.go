package meta

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type Remote struct {
	MD5     string
	Version int64
	Mime    string

	Rev string
}

type Sent struct {
	Name    string
	Parent  string
	Trashed bool
	Mtime   time.Time
}

const sentSet = `r_name = :sent_name, r_parent = :sent_parent, r_trashed = :sent_trashed, r_mtime = :sent_mtime,
	meta_dirty = CASE WHEN name = :sent_name AND parent = :sent_parent AND trashed = :sent_trashed AND mtime = :sent_mtime
		THEN 0 ELSE meta_dirty END`

func sentArgs(m Sent) []any {
	return []any{sql.Named("sent_name", m.Name), sql.Named("sent_parent", m.Parent),
		sql.Named("sent_trashed", m.Trashed), sql.Named("sent_mtime", nanos(m.Mtime))}
}

func remoteArgs(r Remote) []any {
	return []any{sql.Named("md5", r.MD5), sql.Named("rev", r.Rev), sql.Named("version", r.Version), sql.Named("mime",
		r.Mime)}
}

func (s *Store) MarkCreated(ino Ino, r Remote, m Sent) error {
	return s.inTx("mark created", func(tx txn) error {
		args := append(append(remoteArgs(r), sql.Named("ino", ino)), sentArgs(m)...)
		_, err := tx.Exec(`UPDATE items SET r_exists = 1, md5 = :md5, rev = :rev, r_md5 = :md5, r_version = :version, mime `+
			`= :mime, `+
			sentSet+` WHERE ino = :ino`, args...)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM upload_sessions WHERE ino = ?`, ino); err != nil {
			return err
		}
		return nil
	})
}

type Session struct {
	Address      string
	Name, Parent string
	Mtime        time.Time
}

func (s *Store) UploadSession(ino Ino) (Session, error) {
	var ses Session
	var mtime int64
	err := s.r.QueryRow(`SELECT address, name, parent, mtime FROM upload_sessions WHERE ino = ?`, ino).
		Scan(&ses.Address, &ses.Name, &ses.Parent, &mtime)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, nil
	}
	if err != nil {
		return Session{}, fmt.Errorf("meta: upload session: %w", err)
	}
	ses.Mtime = fromNanos(mtime).UTC()
	return ses, nil
}

func (s *Store) SetUploadSession(ino Ino, ses Session) error {
	return exec(s.w, "set upload session", `INSERT INTO upload_sessions(ino, address, name, parent, mtime)
		SELECT ?1, ?2, ?3, ?4, ?5 WHERE EXISTS (SELECT 1 FROM items WHERE ino = ?1 AND dirty = 1)
		ON CONFLICT(ino) DO UPDATE SET address = excluded.address, name = excluded.name,
		parent = excluded.parent, mtime = excluded.mtime`,
		ino, ses.Address, ses.Name, ses.Parent, nanos(ses.Mtime))
}

func (s *Store) ClearUploadSession(ino Ino) error {
	return exec(s.w, "clear upload session", `DELETE FROM upload_sessions WHERE ino = ?`, ino)
}

func (s *Store) MarkMetaSent(ino Ino, r Remote, m Sent) error {
	args := append([]any{sql.Named("version", r.Version), sql.Named("ino", ino)}, sentArgs(m)...)
	return exec(s.w, "mark metadata sent",
		`UPDATE items SET version = CASE WHEN dirty = 0 THEN :version ELSE version END, r_version = :version,
		dirty = CASE WHEN :sent_trashed THEN 0 ELSE dirty END, `+sentSet+` WHERE ino = :ino`, args...)
}

func (s *Store) MarkCreateTried(ino Ino) error {
	return exec(s.w, "mark create tried", `UPDATE items SET create_tried = 1 WHERE ino = ?`, ino)
}

func (s *Store) ClearCreateTried(ino Ino) error {
	return exec(s.w, "clear create tried", `UPDATE items SET create_tried = 0 WHERE ino = ?`, ino)
}

func (s *Store) MarkClean(ino Ino, r Remote, m Sent, size int64) error {
	tx, err := s.w.Begin()
	if err != nil {
		return fmt.Errorf("meta: mark clean: %w", err)
	}
	defer tx.Rollback()
	args := append(append(remoteArgs(r), sql.Named("size", size), sql.Named("ino", ino)), sentArgs(m)...)
	_, err = tx.Exec(`UPDATE items SET dirty = 0, r_exists = 1, size = :size, md5 = :md5, rev = :rev, version = :version,
		r_md5 = :md5, r_version = :version, mime = :mime, `+sentSet+` WHERE ino = :ino`, args...)
	if err != nil {
		return fmt.Errorf("meta: mark clean: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM pieces WHERE ino = ?`, ino); err != nil {
		return fmt.Errorf("meta: mark clean: %w", err)
	}
	if size > 0 {
		if _, err := tx.Exec(`INSERT INTO pieces(ino, off, len) VALUES (?, 0, ?)`, ino, size); err != nil {
			return fmt.Errorf("meta: mark clean: %w", err)
		}
	}

	return commit(tx)
}

func (s *Store) RetryNetFailures() error {
	return s.inTx("retry network failures", func(tx txn) error {
		if _, err := tx.Exec(`UPDATE ops SET next_try = 0, net = 0 WHERE net = 1`); err != nil {
			return err
		}
		return nil
	})
}
