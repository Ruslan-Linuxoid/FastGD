package core

import (
	"log/slog"
	"slices"
	"sync"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

const longestWait = time.Hour

const clockSlack = time.Minute

type queue struct {
	store queueRecords
	log   *slog.Logger

	picking sync.Mutex
	mu      sync.Mutex
	held    map[meta.Ino]bool
}

type queueRecords interface {
	ReadyOps(now time.Time, limit int, skip []meta.Ino) ([]meta.Op, error)
	StartOp(id int64) error
	RequeueOp(op meta.Op) error
	RequeueSending(sending ...meta.Ino) error
	ForgetFutureWaits(now time.Time, longest time.Duration) error
}

func newQueue(store queueRecords, log *slog.Logger) *queue {
	return &queue{store: store, log: log, held: map[meta.Ino]bool{}}
}

func (q *queue) hold(inos ...meta.Ino) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, ino := range inos {
		if q.held[ino] {
			return false
		}
	}
	for _, ino := range inos {
		q.held[ino] = true
	}
	return true
}

func (q *queue) release(inos ...meta.Ino) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, ino := range inos {
		delete(q.held, ino)
	}
}

func (q *queue) holding(ino meta.Ino) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.held[ino]
}

func (q *queue) due(now time.Time, busy []meta.Ino) []meta.Op {
	q.picking.Lock()
	defer q.picking.Unlock()
	q.mu.Lock()
	skip := slices.Clone(busy)
	for ino := range q.held {
		skip = append(skip, ino)
	}
	q.mu.Unlock()

	if err := q.store.ForgetFutureWaits(now, longestWait+clockSlack); err != nil {
		q.log.Error("forget the waits a clock that went back left", "err", err)
	}

	if err := q.store.RequeueSending(skip...); err != nil {
		q.log.Error("requeue the operations left being sent", "err", err)
	}
	ops, err := q.store.ReadyOps(now, readyBatch, skip)
	if err != nil {
		q.log.Error("list the operations due", "err", err)
		return nil
	}
	var out []meta.Op
	for _, op := range ops {
		if q.hold(op.Ino) {
			out = append(out, op)
		}
	}
	for _, op := range out {
		if err := q.store.StartOp(op.ID); err != nil {
			q.log.Error("start an operation", "id", op.ID, "err", err)
		}
	}
	return out
}

func (q *queue) idle() bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.held) == 0
}

func (q *queue) requeue(op meta.Op) {
	if err := q.store.RequeueOp(op); err != nil {
		q.log.Error("requeue an operation", "id", op.ID, "err", err)
	}
}
