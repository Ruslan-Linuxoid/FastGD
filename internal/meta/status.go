package meta

import (
	"database/sql"
	"fmt"
	"net/url"
	"time"
)

type ReadOnly struct {
	s *Store
}

func OpenReadOnly(path string) (*ReadOnly, error) {
	db, err := sql.Open("sqlite", uri(path, url.Values{"mode": {"ro"}, "_pragma": {"busy_timeout(10000)"}}))
	if err != nil {
		return nil, fmt.Errorf("meta: %w", err)
	}
	var v int
	if err := (conn{db}).QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		db.Close()
		return nil, fmt.Errorf("meta: open %s: %w", path, err)
	}
	if v != schemaVersion {
		db.Close()
		return nil, fmt.Errorf("meta: %s has schema %d, this build reads %d", path, v, schemaVersion)
	}
	return &ReadOnly{s: &Store{r: conn{db}}}, nil
}

func (ro *ReadOnly) Close() error { return ro.s.r.Close() }

func (ro *ReadOnly) CachedPieces() ([]CachedPiece, error) { return ro.s.CachedPieces() }

func (ro *ReadOnly) Dirty() ([]Item, error) { return ro.s.Dirty() }

func (ro *ReadOnly) Unsent() ([]Item, error) {
	rows, err := ro.s.r.Query(`SELECT `+itemCols+` FROM items
		WHERE `+sqlContentLocal+` AND service = 0 AND note_for = 0 AND trashed = 0 AND kind = ? ORDER BY ino`, KindFile)
	if err != nil {
		return nil, fmt.Errorf("meta: unsent: %w", err)
	}
	out, err := scanItems(rows)
	if err != nil {
		return nil, fmt.Errorf("meta: unsent: %w", err)
	}
	return out, nil
}

func (ro *ReadOnly) LastPath(id string) (string, error) { return ro.s.LastPath(id) }

type Failing struct {
	Ino  Ino
	Path string

	Attempts  int
	NextTry   time.Time
	LastError string
}

type Status struct {
	DirtyFiles int
	DirtyBytes int64

	Waiting int

	LostFiles int

	Discarded     time.Time
	DiscardedWhat string

	Undone     time.Time
	UndoneWhat string

	UploadsRefused string

	LoginNeeded time.Time

	CacheLimit   int64
	CacheLimitBy string
	Failing      []Failing
	LastPoll     time.Time

	DayUnits int64
	DayBytes int64

	DownloadRefused time.Time
	RefusedPath     string

	AbuseRefused time.Time
	AbusivePath  string
}

func (ro *ReadOnly) ReadStatus(since time.Time) (Status, error) {
	var st Status
	tx, err := ro.s.r.Begin()
	if err != nil {
		return st, fmt.Errorf("meta: status: %w", err)
	}
	defer tx.Rollback()
	if err := readCounts(tx, &st); err != nil {
		return st, err
	}
	failing, err := readFailing(tx)
	if err != nil {
		return st, err
	}
	if err := readTimes(tx, &st, since); err != nil {
		return st, err
	}
	refusedID, abusiveID, err := readRefusals(tx, &st)
	if err != nil {
		return st, err
	}
	if err := tx.Commit(); err != nil {
		return st, fmt.Errorf("meta: status: %w", err)
	}
	if refusedID != "" {
		st.RefusedPath = ro.pathOr(refusedID, refusedID)
	}
	if abusiveID != "" {
		st.AbusivePath = ro.pathOr(abusiveID, abusiveID)
	}
	st.Failing = ro.withPaths(failing)
	return st, nil
}

func (ro *ReadOnly) withPaths(failing []Failing) []Failing {
	var out []Failing
	for _, f := range failing {
		it, err := ro.s.ByIno(f.Ino)
		if err != nil {

			continue
		}
		f.Path = ro.pathOr(it.ID, it.Name)
		out = append(out, f)
	}
	return out
}

func (ro *ReadOnly) pathOr(id, fallback string) string {
	if p, err := ro.s.Path(id); err == nil {
		return p
	}
	return fallback
}
