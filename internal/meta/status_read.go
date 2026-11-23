package meta

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

func readCounts(q db, st *Status) error {

	err := q.QueryRow(`SELECT count(*), coalesce(sum(size), 0) FROM items WHERE dirty = 1 AND trashed = 0 `+
		`AND `+sqlGoesToDrive).
		Scan(&st.DirtyFiles, &st.DirtyBytes)
	if err != nil {
		return fmt.Errorf("meta: status: %w", err)
	}
	if err := q.QueryRow(`SELECT count(*) FROM ops`).Scan(&st.Waiting); err != nil {
		return fmt.Errorf("meta: status: %w", err)
	}
	err = q.QueryRow(`SELECT count(*) FROM items WHERE `+sqlLost+` AND kind = ? AND trashed = 0 AND note_for = 0`,
		KindFile).
		Scan(&st.LostFiles)
	if err != nil {
		return fmt.Errorf("meta: status: %w", err)
	}
	return nil
}

func readFailing(q db) ([]Failing, error) {
	rows, err := q.Query(`SELECT o.ino, o.attempts, o.next_try, o.last_error FROM ops o
		WHERE o.attempts > 0 AND o.id = (SELECT min(id) FROM ops WHERE ino = o.ino) ORDER BY o.next_try`)
	if err != nil {
		return nil, fmt.Errorf("meta: status: %w", err)
	}
	defer rows.Close()
	var failing []Failing
	for rows.Next() {
		var f Failing
		var next int64
		if err := rows.Scan(&f.Ino, &f.Attempts, &next, &f.LastError); err != nil {
			rows.Close()
			return nil, fmt.Errorf("meta: status: %w", err)
		}
		f.NextTry = fromNanos(next)
		failing = append(failing, f)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("meta: status: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("meta: status: %w", err)
	}
	return failing, nil
}

func readTimes(q db, st *Status, since time.Time) error {
	discarded, err := stateIn(q, StateDiscarded)
	if err != nil {
		return err
	}

	if at, what, ok := strings.Cut(discarded, " "); ok {
		if n, err := strconv.ParseInt(at, 10, 64); err == nil {
			st.Discarded, st.DiscardedWhat = fromNanos(n), what
		}
	}
	poll, err := stateIn(q, StateLastPoll)
	if err != nil {
		return fmt.Errorf("meta: status: %w", err)
	}
	st.LastPoll = parseNanos(poll)
	if st.DayUnits, st.DayBytes, err = usageSince(q, since); err != nil {
		return err
	}
	undone, err := stateIn(q, StateUndone)
	if err != nil {
		return fmt.Errorf("meta: status: %w", err)
	}
	if at, what, ok := parseStamped(undone); ok {
		st.Undone, st.UndoneWhat = at, what
	}
	if st.UploadsRefused, err = stateIn(q, StateRefused); err != nil {
		return fmt.Errorf("meta: status: %w", err)
	}
	return readCacheLimit(q, st)
}

func readCacheLimit(q db, st *Status) error {
	limit, err := stateIn(q, StateCacheLimit)
	if err != nil {
		return fmt.Errorf("meta: status: %w", err)
	}
	if bytes, by, ok := strings.Cut(limit, " "); ok {
		if n, err := strconv.ParseInt(bytes, 10, 64); err == nil {
			st.CacheLimit, st.CacheLimitBy = n, by
		}
	}
	return nil
}

func readRefusals(q db, st *Status) (refusedID, abusiveID string, err error) {
	login, err := stateIn(q, StateLoginNeeded)
	if err != nil {
		return "", "", fmt.Errorf("meta: status: %w", err)
	}
	if login != "" {
		st.LoginNeeded = parseNanos(login)
	}
	refusal, err := stateIn(q, StateDownloadRefused)
	if err != nil {
		return "", "", fmt.Errorf("meta: status: %w", err)
	}
	if at, id, ok := parseStamped(refusal); ok {
		st.DownloadRefused, refusedID = at, id
	}
	abuse, err := stateIn(q, StateAbuseRefused)
	if err != nil {
		return "", "", fmt.Errorf("meta: status: %w", err)
	}
	if at, id, ok := parseStamped(abuse); ok {
		st.AbuseRefused, abusiveID = at, id
	}
	return refusedID, abusiveID, nil
}
