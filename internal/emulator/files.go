package emulator

import (
	"slices"
	"strings"
	"time"
)

type File struct {
	ID       string
	Name     string
	MimeType string

	Parents []string

	Trashed  bool
	Content  []byte
	Version  int64
	Created  time.Time
	Modified time.Time

	ShortcutTarget string

	CannotModify, CannotTrash, CannotRename, CannotMove, CannotAddChildren, CannotRemoveChildren bool

	CannotMoveWithinDrive bool

	RightsLeftOut []string

	CannotDownload bool

	CannotReadRevisions bool

	HeadRevision string

	md5 string

	Abusive, NotMine bool
}

func (f *File) abuseRefused(ack bool) bool { return f.Abusive && (!ack || f.NotMine) }

func (f *File) isGoogleType() bool { return strings.HasPrefix(f.MimeType, googlePrefix) }

func (e *Emulator) Put(f File) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if f.ID == "" {
		f.ID = e.newID()
	}
	now := e.now()
	if f.Version == 0 {
		f.Version = 1
	}
	if f.Created.IsZero() {
		f.Created = now
	}
	if f.Modified.IsZero() {
		f.Modified = f.Created
	}
	f.Parents = append([]string(nil), f.Parents...)
	f.Content = append([]byte(nil), f.Content...)
	f.md5 = md5Hex(f.Content)
	e.revise(&f)
	if _, ok := e.files[f.ID]; !ok {
		e.order = append(e.order, f.ID)
	}
	e.files[f.ID] = &f
	e.changes = append(e.changes, change{id: f.ID})
	return f.ID
}

func (e *Emulator) Get(id string) (File, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	f, ok := e.files[id]
	if !ok {
		return File{}, false
	}
	return f.clone(), true
}

func (f *File) clone() File {
	c := f.bare()
	c.Content = slices.Clone(f.Content)
	return c
}

func (f *File) bare() File {
	c := *f
	c.Parents = slices.Clone(f.Parents)
	return c
}

func (e *Emulator) trashed(f *File) bool {
	for seen := 0; f != nil && seen < 10000; seen++ {
		if f.Trashed {
			return true
		}
		if len(f.Parents) == 0 {
			return false
		}
		f = e.files[f.Parents[0]]
	}
	return false
}
