package edited

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/cache"
	"github.com/Ruslan-Linuxoid/FastGD/internal/clock"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

type Upload struct {
	Item meta.Item

	Gen  int64
	Size int64

	Remote meta.Remote

	SentMD5 string

	SentMeta meta.Sent

	Same bool
}

type Records interface {
	Store
	ByID(id string) (meta.Item, error)
	Dirty() ([]meta.Item, error)
	MarkDirty(ino meta.Ino, base int64, tail bool) error
	SetLocal(ino meta.Ino, size int64, mtime time.Time, gen int64, uploadAfter time.Time) error
	ClearUploadSession(ino meta.Ino) error
	UploadSession(ino meta.Ino) (meta.Session, error)
}

type Marks interface {
	MarkCreated(ino meta.Ino, r meta.Remote, m meta.Sent) error
	MarkClean(ino meta.Ino, r meta.Remote, m meta.Sent, size int64) error
	SetMtime(ino meta.Ino, mtime time.Time) error
	SetRev(ino meta.Ino, rev string) error
	KeptRevision(id string) (string, error)
	KeptRevisions() (map[string]string, error)
	ForgetKeptRevision(id string) error
}

type Disk interface {
	Clean
	LocalSize(id string) (int64, error)
	CreateLocal(id string) error
	Adopt(ino meta.Ino, size int64) error
}

type Options struct {
	Disk   Disk
	Store  Records
	Marks  Marks
	Drive  Drive
	Source cache.Source
	Clock  clock.Clock
	Log    *slog.Logger

	Room func(it meta.Item, old, size int64) error

	UploadDue func(ino meta.Ino) time.Time

	Backoff func(n int) time.Duration

	Resized func(ino meta.Ino)

	RecoverFault func(step string, it meta.Item) error

	ReadAnew func(ctx context.Context, id string) error

	GiveUpBase func(ctx context.Context, ino meta.Ino, cause error) error
}

type edits struct {
	opt Options

	smu    sync.Mutex
	states map[meta.Ino]*State

	notTakenUp map[meta.Ino]bool
	takingUp   sync.Mutex

	waiting atomic.Int64

	cleaning sync.RWMutex

	beforeRecord func() error

	beforeFetch func()

	syncLocal func(id string) error
	bases     bases
}

func Open(o Options, syncLocal func(id string) error) *Files {
	m := New(o.Disk, o.Store, o.Source)
	m.edits = edits{
		opt: o, states: map[meta.Ino]*State{}, notTakenUp: map[meta.Ino]bool{}, syncLocal: syncLocal,
		bases: bases{wanted: map[meta.Ino]*baseTry{}},
	}
	return m
}

func (m *Files) BeforeRecord(fn func() error) { m.beforeRecord = fn }

func (m *Files) BeforeFetch(fn func()) { m.beforeFetch = fn }

func (m *Files) SyncLocal(fn func(id string) error) { m.syncLocal = fn }

func (m *Files) Flush(id string) error { return m.syncLocal(id) }

func (m *Files) Bytes() int64 { return m.waiting.Load() }

func (m *Files) HoldClean() (release func()) {
	m.cleaning.Lock()
	return m.cleaning.Unlock
}

func (m *Files) CleanSettled() (release func()) {
	m.cleaning.RLock()
	return m.cleaning.RUnlock
}

func (m *Files) State(it meta.Item) *State {
	m.smu.Lock()
	defer m.smu.Unlock()
	st := m.states[it.Ino]
	if st == nil {
		st = &State{of: m, ino: it.Ino, gen: it.Gen, dirty: it.Dirty, size: it.Size, mtime: it.Mtime,
			basing: make(chan struct{}, 1)}
		st.show()
		m.states[it.Ino] = st
	}
	return st
}

func (m *Files) StateIf(ino meta.Ino) *State {
	m.smu.Lock()
	defer m.smu.Unlock()
	return m.states[ino]
}

func (m *Files) Lock(it meta.Item) *State {
	for {
		st := m.State(it)
		st.mu.Lock()
		if !st.released {
			return st
		}
		st.Unlock()
	}
}

func (m *Files) FileOf(it meta.Item) cache.File {
	f := cleanFile(it)
	if st := m.StateIf(it.Ino); st != nil {
		st.mu.Lock()

		if st.dirty {
			f.Size = st.size
		}
		st.Unlock()
	}
	if it.ContentLocal() {
		f.Base = 0
	}
	return f
}

func cleanFile(it meta.Item) cache.File {
	return cache.File{Ino: it.Ino, ID: it.ID, Size: it.Size, Base: it.Size, MD5: it.MD5, Rev: it.Rev}
}

func DriveTime(t time.Time) time.Time { return t.Truncate(time.Millisecond) }
