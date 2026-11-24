package drive

import (
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"google.golang.org/api/googleapi"
)

const (
	shrinkEvery = 5 * time.Minute
	growEvery   = 10 * time.Minute

	pageStep = 2
)

type pageSize struct {
	log *slog.Logger

	mu   sync.Mutex
	size int

	shrunk, changed time.Time
}

func newPageSize(log *slog.Logger) *pageSize { return &pageSize{log: log, size: listPageSize} }

func (p *pageSize) get() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.size
}

func (p *pageSize) answered(err error, now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case err == nil:
		if p.size >= listPageSize || now.Sub(p.changed) < growEvery {
			return
		}
		p.set(min(p.size*pageStep, listPageSize), now)
	case serverTrouble(err):
		if p.size <= 1 || !p.shrunk.IsZero() && now.Sub(p.shrunk) < shrinkEvery {
			return
		}
		p.set(max(p.size/pageStep, 1), now)
		p.shrunk = now
	}
}

func (p *pageSize) set(n int, now time.Time) {
	p.log.Info("the page of changes asked for changes its size", "from", p.size, "to", n)
	p.size, p.changed = n, now
}

func serverTrouble(err error) bool {
	var gerr *googleapi.Error
	return errors.As(err, &gerr) && gerr.Code >= http.StatusInternalServerError
}
