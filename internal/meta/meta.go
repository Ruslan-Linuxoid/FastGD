package meta

import (
	"errors"
	"sync/atomic"
	"time"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("meta: not found")

var ErrNotShown = errors.New("meta: not shown in the mount")

type Item struct {
	Ino     Ino
	ID      string
	Kind    Kind
	Mime    string
	Name    string
	Parent  string
	Trashed bool
	Size    int64

	MD5     string
	Version int64
	Rev     string
	Mtime   time.Time
	Ctime   time.Time

	ShortcutTarget string

	RExists bool

	Dirty bool

	RMD5     string
	RVersion int64

	MetaDirty bool
	RName     string
	RParent   string
	RTrashed  bool
	RMtime    time.Time

	Gen int64

	UploadAfter time.Time

	Denied Denied

	Base int64

	BaseTail bool

	CreateTried bool

	Lost bool

	Service bool

	Shared bool

	shownAs string
}

type Entry struct {
	Item

	Display string

	Link string
}

type Store struct {
	w conn
	r conn

	readFault atomic.Pointer[func(Item) error]

	shared sharedList
}

func (s *Store) FailItemReads(fn func(Item) error) {
	if fn == nil {
		s.readFault.Store(nil)
		return
	}
	s.readFault.Store(&fn)
}
