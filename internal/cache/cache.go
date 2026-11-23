package cache

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"github.com/Ruslan-Linuxoid/FastGD/internal/speed"
)

const prefetchWorkers = 4

const joinAbove = 128

const readTries = 3

var errClosed = errors.New("cache: closed")

var ErrStale = errors.New("cache: the file changed in Drive since it was opened")

var errSwitched = errors.New("cache: version replaced during the read")

var errReplaced = errors.New("cache: the sparse file was deleted or replaced during the download")

type Source func(ctx context.Context, id, rev string, off, length int64, sink func([]byte) error) error

type File struct {
	Ino  meta.Ino
	ID   string
	Size int64

	Base int64

	MD5 string

	Rev string

	Gen uint64
}

type Cache struct {
	opt Options
	log *slog.Logger

	genNums atomic.Uint64

	bg     context.Context
	stopBg context.CancelFunc
	wg     sync.WaitGroup
	sem    chan struct{}
	meter  speed.Meter

	mu     sync.Mutex
	closed bool

	gens map[meta.Ino]*gen

	locks map[meta.Ino]*sync.RWMutex

	room room

	evicting sync.Mutex
	trims    chan struct{}

	worths  worths
	evicted time.Time
	waiting bool

	lowDisk atomic.Bool

	free atomic.Int64

	made map[string]bool

	strays map[string]int64

	fetching map[*gen]struct{}

	dirtyMoved atomic.Bool

	driveFiles atomic.Int64
}

func (c *Cache) Close() {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	c.stopBg()
	c.wg.Wait()
}

func (c *Cache) path(id string) string { return PathIn(c.opt.Dir, id) }

const shardLen = 2

func PathIn(dir, id string) string {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, "/\\\x00") {
		return filepath.Join(dir, "\x00")
	}
	return filepath.Join(dir, id[:min(shardLen, len(id))], id)
}

func (c *Cache) fileLock(ino meta.Ino) *sync.RWMutex {
	c.mu.Lock()
	defer c.mu.Unlock()
	l := c.locks[ino]
	if l == nil {
		l = &sync.RWMutex{}
		c.locks[ino] = l
	}
	return l
}

func (c *Cache) isCurrent(g *gen, ino meta.Ino) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.gens[ino] == g
}

func (c *Cache) isStale(g *gen) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return g.stale
}

func (c *Cache) isLocal(g *gen) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return g.local
}
