package meta

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type db interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

func exec(q db, op, query string, args ...any) error {
	if _, err := q.Exec(query, args...); err != nil {
		return fmt.Errorf("meta: %s: %w", op, err)
	}
	return nil
}

func scalar[T any](q db, op, query string, args ...any) (T, error) {
	var v T
	if err := q.QueryRow(query, args...).Scan(&v); err != nil {
		return v, fmt.Errorf("meta: %s: %w", op, err)
	}
	return v, nil
}

func putState(q db, key, value string) error {
	_, err := q.Exec(`INSERT INTO state(key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value)
	return err
}

const keptRevisionPrefix = "kept_revision:"

func (s *Store) KeepRevision(id, rev string) error { return s.SetState(keptRevisionPrefix+id, rev) }

func (s *Store) KeptRevision(id string) (string, error) {
	rev, _, err := s.GetState(keptRevisionPrefix + id)
	return rev, err
}

func (s *Store) KeptRevisions() (map[string]string, error) {
	return s.statesUnder(keptRevisionPrefix)
}

func (s *Store) ForgetKeptRevision(id string) error { return s.DeleteState(keptRevisionPrefix + id) }

func stateIn(q db, key string) (string, error) {
	var v string
	err := q.QueryRow(`SELECT value FROM state WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

func stamped(at time.Time, what string) string { return fmt.Sprintf("%d %s", nanos(at), what) }

func parseStamped(v string) (at time.Time, what string, ok bool) {
	ns, what, ok := strings.Cut(v, " ")
	if !ok {
		return time.Time{}, "", false
	}
	return parseNanos(ns), what, true
}

func parseNanos(v string) time.Time {
	ns, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return time.Time{}
	}
	return fromNanos(ns)
}

const StateEmail = "account_email"

const StateRightsUnknown = "rights_unknown"

const StateRefused = "uploads_refused"

const StateDownloadRefused = "download_refused"

func (s *Store) SetDownloadRefused(at time.Time, id string) error {
	return s.SetState(StateDownloadRefused, stamped(at, id))
}

func (s *Store) Refused(state string) (string, bool) {
	v, ok, err := s.GetState(state)
	if err != nil || !ok {
		return "", false
	}
	_, id, ok := parseStamped(v)
	return id, ok
}

const StateAbuseRefused = "abuse_refused"

func (s *Store) RefuseAbusive(at time.Time, id string) error {
	return s.inTx("refuse an abusive file", func(tx txn) error {
		if _, err := tx.Exec(`UPDATE items SET denied = denied | ? WHERE id = ?`, DenyDownload, id); err != nil {
			return err
		}
		if err := putState(tx, StateAbuseRefused, stamped(at, id)); err != nil {
			return err
		}
		return nil
	})
}

func (s *Store) SetLastPoll(t time.Time) error {
	return s.SetState(StateLastPoll, strconv.FormatInt(nanos(t), 10))
}

func (s *Store) SetQuota(limit, usage, files int64) error {
	return s.SetState(StateQuota, fmt.Sprintf("%d %d %d", limit, usage, files))
}

func (s *Store) Quota() (limit, usage, files int64, filesKnown, ok bool) {
	v, found, err := s.GetState(StateQuota)
	if err != nil || !found {
		return 0, 0, 0, false, false
	}
	parts := strings.Fields(v)
	if len(parts) != 2 && len(parts) != 3 {
		return 0, 0, 0, false, false
	}
	n := make([]int64, len(parts))
	for i, p := range parts {
		if n[i], err = strconv.ParseInt(p, 10, 64); err != nil {
			return 0, 0, 0, false, false
		}
	}
	if len(n) == 2 {
		return n[0], n[1], 0, false, true
	}
	return n[0], n[1], n[2], true, true
}

const StateCacheLimit = "cache_limit"

const StateLoginNeeded = "login_needed"

const StateUndone = "undone"

func (s *Store) SetUndone(at time.Time, path, why string) error {
	return s.SetState(StateUndone, stamped(at, path+": "+why))
}

const StateDiscarded = "discarded"

const StateRestoreFrom = "restore_from"

func (s *Store) SetDiscarded(at time.Time, aside, why string) error {
	return s.SetState(StateDiscarded, stamped(at, aside+": "+why))
}

const StateHoleUnit = "hole_unit"

const StateFields = "fields"

const (
	StateRootID       = "root_id"
	StateChangesToken = "changes_token"
	StateLoaded       = "initial_load_done"

	StateLastPoll = "last_poll"

	StateQuota = "quota"
)

func (s *Store) GetState(key string) (string, bool, error) {
	var v string
	err := s.r.QueryRow(`SELECT value FROM state WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("meta: state %s: %w", key, err)
	}
	return v, true, nil
}

func (s *Store) SetState(key, value string) error {
	if err := putState(s.w, key, value); err != nil {
		return fmt.Errorf("meta: set state %s: %w", key, err)
	}
	return nil
}

func (s *Store) DeleteState(key string) error {
	return exec(s.w, "delete state "+key, `DELETE FROM state WHERE key = ?`, key)
}

func (s *Store) statesUnder(prefix string) (map[string]string, error) {
	rows, err := s.r.Query(`SELECT key, value FROM state WHERE key LIKE ?`, prefix+"%")
	if err != nil {
		return nil, fmt.Errorf("meta: state %s: %w", prefix, err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("meta: state %s: %w", prefix, err)
		}
		out[strings.TrimPrefix(key, prefix)] = value
	}
	return out, rows.Err()
}
