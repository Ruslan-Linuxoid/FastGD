package edited

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

var ErrDenied = errors.New("edited: the content of the file may not be changed")

var errNotTakenUp = errors.New("the state of the file after the last run is not taken up")

var ErrMD5 = errors.New("md5 in Drive differs from the bytes sent")

type State struct {
	of  *Files
	ino meta.Ino
	mu  sync.Mutex

	stale atomic.Bool

	dropped atomic.Bool

	gen     int64
	dirty   bool
	writers int

	unflushed bool
	lastWrite time.Time
	size      int64
	mtime     time.Time

	md5Fails int

	savedGen int64

	session bool

	released bool

	unrecorded *Upload

	counted int64

	basing chan struct{}

	seen atomic.Pointer[sizeTime]
}

type sizeTime struct {
	size  int64
	mtime time.Time
	local bool
}

func (st *State) Unlock() {
	if st.dropped.Load() {
		st.released = true
		st.of.count(st, meta.Item{})
	} else if st.stale.Swap(false) {
		st.of.recounted(st)
	}
	st.mu.Unlock()
}

func (st *State) Writers() int { return st.writers }

func (st *State) Unrecorded() *Upload { return st.unrecorded }

func (m *Files) Snap(it meta.Item) (st *State, gen, size int64, mtime time.Time) {
	st = m.Lock(it)
	defer st.Unlock()
	return st, st.gen, st.size, st.mtime
}

func (st *State) Snapshot() (gen, size int64, mtime time.Time) {
	st.mu.Lock()
	defer st.Unlock()
	return st.gen, st.size, st.mtime
}

func (st *State) Outdated(gen int64) bool {
	st.mu.Lock()
	defer st.Unlock()
	return st.gen != gen
}

func (st *State) KeepSession(gen int64, save func() error) (bool, error) {
	st.mu.Lock()
	defer st.Unlock()
	if st.gen != gen {
		return false, nil
	}
	if err := save(); err != nil {
		return false, err
	}
	st.session = true
	return true, nil
}

func (st *State) Damaged() int {
	st.mu.Lock()
	defer st.Unlock()
	st.md5Fails++
	return st.md5Fails
}

func (st *State) Sound() {
	st.mu.Lock()
	defer st.Unlock()
	st.md5Fails = 0
}

func (st *State) Pending() *Upload {
	st.mu.Lock()
	defer st.Unlock()
	return st.unrecorded
}

func (m *Files) Seen(ino meta.Ino) (size int64, mtime time.Time, ok bool) {
	st := m.StateIf(ino)
	if st == nil {
		return 0, time.Time{}, false
	}

	seen := st.seen.Load()
	return seen.size, seen.mtime, seen.local
}

func (m *Files) SetMtime(ino meta.Ino, mtime time.Time) {
	for {
		st := m.StateIf(ino)
		if st == nil {
			return
		}
		st.mu.Lock()
		if st.released {

			st.Unlock()
			continue
		}
		st.mtime = mtime
		st.show()
		st.Unlock()
		return
	}
}

func (st *State) show() { st.seen.Store(&sizeTime{st.size, st.mtime, st.dirty}) }
