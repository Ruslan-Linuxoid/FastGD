package meta

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
)

var ErrNewerSchema = errors.New("meta: the database is of a newer build")

func dsn(path string, readOnly bool) string {
	q := url.Values{"_pragma": {"busy_timeout(10000)", "journal_mode(WAL)", "synchronous(FULL)"}}
	if readOnly {
		q["_pragma"] = append(q["_pragma"], "query_only(1)")
	}
	return uri(path, q)
}

func uri(path string, q url.Values) string {
	u := url.URL{Scheme: "file", Path: path, RawQuery: q.Encode()}
	return u.String()
}

const readConns = 8

func Open(path string) (*Store, error) {
	w, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		return nil, fmt.Errorf("meta: %w", err)
	}
	w.SetMaxOpenConns(1)
	s := &Store{w: conn{w}}
	if err := s.migrate(path); err != nil {
		w.Close()
		return nil, err
	}
	r, err := sql.Open("sqlite", dsn(path, true))
	if err != nil {
		w.Close()
		return nil, fmt.Errorf("meta: %w", err)
	}
	r.SetMaxOpenConns(readConns)
	s.r = conn{r}
	return s, nil
}

func (s *Store) migrate(path string) error {

	if _, err := s.w.Exec(`CREATE TABLE IF NOT EXISTS touched (ino INTEGER PRIMARY KEY) WITHOUT ROWID`); err != nil {
		return fmt.Errorf("meta: open %s: %w", path, err)
	}
	var v int
	if err := s.w.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		return fmt.Errorf("meta: open %s: %w", path, err)
	}
	v, err := s.upgrade(path, v)
	if err != nil {
		return err
	}
	if err := checkSchema(path, v); err != nil {
		return err
	}
	return s.createSchema()
}

type step struct {
	from, to int
	run      func(s *Store) error
}

func steps() []step {
	return []step{
		{schemaV6, schemaV7, (*Store).fromBlocks},
		{schemaV7, schemaV8, (*Store).addRights},
		{schemaV8, schemaV9, (*Store).addBase},
		{schemaV9, schemaV10, (*Store).addValues},
		{schemaV10, schemaV11, (*Store).addCreateTried},
		{schemaV11, schemaV12, (*Store).toLinkFiles},
		{schemaV12, schemaV13, (*Store).addRev},
		{schemaV13, schemaV14, (*Store).addLost},
		{schemaV14, schemaV15, nil},
		{schemaV15, schemaV16, (*Store).dropValues},
		{schemaV16, schemaV17, (*Store).addService},

		{schemaV17, schemaV18, func(s *Store) error { return s.SetState(StateRightsUnknown, "1") }},
		{schemaV18, schemaV19, (*Store).plainOps},
		{schemaV19, schemaV20, (*Store).oneCopyOfTries},

		{schemaV20, schemaV21, nil},
		{schemaV21, schemaV22, (*Store).ownShelf},
		{schemaV22, schemaV23, (*Store).countBusy},
		{schemaV23, schemaV24, (*Store).keepNotes},
		{schemaV24, schemaV25, (*Store).keepShownNames},
		{schemaV25, schemaV26, (*Store).addTop},
		{schemaV26, schemaVersion, (*Store).addShared},
	}
}

func (s *Store) upgrade(path string, v int) (int, error) {
	for _, st := range steps() {
		if v != st.from {
			continue
		}
		if st.run != nil {
			if err := st.run(s); err != nil {
				return v, fmt.Errorf("meta: upgrade %s: %w", path, err)
			}
		}
		v = st.to
	}
	return v, nil
}

func checkSchema(path string, v int) error {
	if v > schemaVersion {
		return fmt.Errorf("%w: %s has schema %d, this build knows none after %d", ErrNewerSchema, path, v, schemaVersion)
	}
	if v != 0 && v != schemaVersion {
		return fmt.Errorf("meta: %s has schema %d, this build needs %d: delete it to reload from Drive; the content of "+
			"files that did not reach Drive then stays in content under their fileIds until the cache needs its room", path, v,
			schemaVersion)
	}
	return nil
}

func (s *Store) createSchema() error {
	if _, err := s.w.Exec(schema); err != nil {
		return fmt.Errorf("meta: create schema: %w", err)
	}
	if err := s.countSharedChanges(); err != nil {
		return err
	}
	if err := s.renameGroups(); err != nil {
		return fmt.Errorf("meta: %w", err)
	}

	if err := s.nameAll(); err != nil {
		return fmt.Errorf("meta: %w", err)
	}
	if _, err := s.w.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, schemaVersion)); err != nil {
		return fmt.Errorf("meta: %w", err)
	}
	return nil
}

func (s *Store) Close() error {
	err := s.r.Close()
	if werr := s.w.Close(); err == nil {
		err = werr
	}
	return err
}

const StateDropClean = "drop_clean_content"

const StateDropExports = "drop_exports"
