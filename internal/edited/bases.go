package edited

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/cache"
	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

type bases struct {
	mu     sync.Mutex
	wanted map[meta.Ino]*baseTry
}

type baseTry struct {
	running bool

	failed int
	next   time.Time

	waited int
}

func (m *Files) wantBase(ino meta.Ino) {
	m.bases.mu.Lock()
	defer m.bases.mu.Unlock()
	if m.bases.wanted[ino] == nil {
		m.bases.wanted[ino] = &baseTry{}
	}
}

func (m *Files) dueBases() []meta.Ino {
	now := m.opt.Clock.Now()
	m.bases.mu.Lock()
	defer m.bases.mu.Unlock()
	running := 0
	for _, try := range m.bases.wanted {
		if try.running {
			running++
		}
	}
	var out []meta.Ino
	for ino, try := range m.bases.wanted {
		if running+len(out) >= maxBaseFetches {

			break
		}
		if !try.running && !now.Before(try.next) {
			try.running = true
			out = append(out, ino)
		}
	}
	return out
}

const maxBaseFetches = 4

func (m *Files) StartBases(ctx context.Context, spawn func(func())) {
	for _, ino := range m.dueBases() {
		spawn(func() { m.fetchWanted(ctx, ino) })
	}
}

func baseRetries() int { return cache.TaskRetrySteps() }

func (m *Files) fetchWanted(ctx context.Context, ino meta.Ino) {
	err := m.FetchBase(ctx, ino)
	if !m.baseTried(ctx, ino, err) || m.opt.GiveUpBase == nil {
		return
	}
	gerr := m.opt.GiveUpBase(ctx, ino, err)
	if gerr == nil {
		return
	}
	if ctx.Err() == nil {
		m.opt.Log.Error("move a file changed here, whose Drive version Drive does not give, into Lost and Found",
			"ino", ino, "cause", err, "err", gerr)
	}
	m.bases.mu.Lock()
	defer m.bases.mu.Unlock()
	if _, ok := m.bases.wanted[ino]; !ok {
		m.bases.wanted[ino] = &baseTry{failed: baseRetries(), next: m.opt.Clock.Now().Add(m.opt.Backoff(baseRetries()))}
	}
}

func (m *Files) baseTried(ctx context.Context, ino meta.Ino, err error) bool {
	m.bases.mu.Lock()
	defer m.bases.mu.Unlock()
	try := m.bases.wanted[ino]
	if try == nil {
		return false
	}

	wait := func(n int) time.Time { return m.opt.Clock.Now().Add(max(m.opt.Backoff(n), drive.WaitAsked(err))) }
	switch {
	case settled(err):
		delete(m.bases.wanted, ino)
		return false
	case errors.Is(err, ErrBaseLost):
		delete(m.bases.wanted, ino)
		m.opt.Log.Error("fetch the Drive version of a file changed here", "ino", ino, "err", err)
		return false
	case waitsOut(ctx, err):
		try.running = false
		try.waited++
		try.next = wait(try.waited)
		if try.waited > 1 {

			return false
		}
	case turnedDown(err):
		delete(m.bases.wanted, ino)
		return true
	case try.failed >= baseRetries():
		delete(m.bases.wanted, ino)
		return true
	default:
		try.running = false
		try.waited = 0
		try.failed++
		try.next = wait(try.failed)
	}
	if ctx.Err() == nil {
		m.opt.Log.Warn("fetch the Drive version of a file changed here", "ino", ino, "tries", try.failed, "err", err)
	}
	return false
}

func waitsOut(ctx context.Context, err error) bool {
	return ctx.Err() != nil || drive.IsNetwork(err) || drive.IsLoginExpired(err) || errors.Is(err, drive.ErrDownloadQuota)
}

func turnedDown(err error) bool {
	return drive.IsDenied(err) || errors.Is(err, drive.ErrAbusive)
}

func settled(err error) bool { return err == nil || errors.Is(err, meta.ErrNotFound) }
