package core

import (
	"container/list"
	"sync"
	"time"
)

const (
	refusalEvery = 10 * time.Minute

	refusalsKept = 1024
)

type refusal struct{ id, right, what string }

type refused struct {
	refusal

	told    time.Time
	repeats int
}

type refusals struct {
	mu sync.Mutex

	order *list.List
	byKey map[refusal]*list.Element
}

func newRefusals() *refusals {
	return &refusals{order: list.New(), byKey: map[refusal]*list.Element{}}
}

func (r *refusals) tell(key refusal, now time.Time) (bool, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if el, ok := r.byKey[key]; ok {
		r.order.MoveToFront(el)
		p, _ := el.Value.(*refused)
		if now.Sub(p.told) < refusalEvery {
			p.repeats++
			return false, 0
		}
		n := p.repeats
		p.told, p.repeats = now, 0
		return true, n
	}
	r.byKey[key] = r.order.PushFront(&refused{refusal: key, told: now})
	if r.order.Len() > refusalsKept {
		last := r.order.Remove(r.order.Back())
		p, _ := last.(*refused)
		delete(r.byKey, p.refusal)
	}
	return true, 0
}

func (f *FS) warnRefused(key refusal, msg string, kv ...any) {
	tell, repeats := f.refusals.tell(key, f.clock.Now())
	if !tell {
		return
	}
	if repeats > 0 {
		kv = append(kv, "repeats", repeats)
	}
	f.log.Warn(msg, kv...)
}
