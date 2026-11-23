package core

import (
	"math/rand/v2"
	"sync"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/cache"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

const (
	busyRetries = 5

	retryFirst  = time.Second
	retryFactor = 8
	retryMax    = longestWait

	answersMax = 50
)

type opKind int

const (
	sendsContent opKind = iota
	sendsCreate
	sendsUpdate
	opKinds
)

func kindOf(it meta.Item) opKind {
	switch {
	case it.Kind == meta.KindFile && (it.Dirty || !it.RExists):
		return sendsContent
	case !it.RExists:
		return sendsCreate
	}
	return sendsUpdate
}

type retries struct {
	mu    sync.Mutex
	ops   map[int64]*tried
	kinds [opKinds]pause
}

type tried struct {
	answers int

	again bool
}

type pause struct {
	level int
	until time.Time
}

func (r *retries) of(op int64) *tried {
	if r.ops == nil {
		r.ops = map[int64]*tried{}
	}
	t := r.ops[op]
	if t == nil {
		t = &tried{}
		r.ops[op] = t
	}
	return t
}

func (r *retries) answered(op int64, anew bool) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	t := r.of(op)
	if anew {
		t.answers = 0
	}
	t.answers++
	return t.answers
}

func (r *retries) goesAgain(op int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	t := r.of(op)
	first := !t.again
	t.again = true
	return first
}

func (r *retries) waitFor(kind opKind, started, now time.Time) time.Time {
	r.mu.Lock()
	defer r.mu.Unlock()
	p := &r.kinds[kind]
	if !started.Before(p.until) {
		p.level++
		wait := retryFirst
		for i := 1; i < p.level && wait < retryMax; i++ {
			wait *= retryFactor
		}
		p.until = now.Add(min(wait, retryMax))
	}
	return p.until
}

func (r *retries) passed(kind opKind) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.kinds[kind] = pause{}
}

func (r *retries) forget(op int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.ops, op)
}

func backoff(n int) time.Duration { return backoffIn(n, minBackoff, maxBackoff) }

func taskBackoff(n int) time.Duration {
	return backoffIn(n, cache.TaskRetryFirst, cache.TaskRetryMax)
}

func backoffIn(n int, least, most time.Duration) time.Duration {
	d := most

	const doublings = 20
	if n < doublings {
		d = min(least<<(n-1), most)
	}

	const spread = 5
	lo, hi := d, min(d+d/spread, most)
	if d == most {
		lo = most - most/spread
	}

	return lo + time.Duration(rand.Int64N(int64(hi-lo)+1))
}
