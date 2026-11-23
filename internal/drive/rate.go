package drive

import (
	"context"
	"sync"
	"time"
)

const (
	perSecond      = 30
	leastPerSecond = 0.1
	rateCutEvery   = 5 * time.Second
	rateGrowEvery  = time.Minute
	rateStep       = 1
)

type callRate struct {
	top float64

	mu sync.Mutex

	per float64

	sent []time.Time

	lastRefusal time.Time
	cut         time.Time
	grown       time.Time
}

func (r *callRate) span() (time.Duration, int) {
	d := max(time.Second, time.Duration(float64(time.Second)/r.per))
	return d + d/50, max(1, int(r.per*d.Seconds()))
}

func (r *callRate) grow(now time.Time) {
	from := r.lastRefusal
	if r.grown.After(from) {
		from = r.grown
	}
	for ; r.per < r.top && now.Sub(from) >= rateGrowEvery; from = from.Add(rateGrowEvery) {
		r.per = min(r.top, r.per+rateStep)
		r.grown = from.Add(rateGrowEvery)
	}
}

func (r *callRate) take(ctx context.Context) error {
	for {
		r.mu.Lock()
		now := time.Now()
		r.grow(now)
		over, room := r.span()
		i := 0
		for i < len(r.sent) && now.Sub(r.sent[i]) >= over {
			i++
		}
		r.sent = r.sent[i:]
		if len(r.sent) < room {
			r.sent = append(r.sent, now)
			r.mu.Unlock()
			return nil
		}

		wait := r.sent[len(r.sent)-room].Add(over).Sub(now)
		if r.per < r.top {

			wait = min(wait, time.Second)
		}
		r.mu.Unlock()
		if err := sleep(ctx, wait); err != nil {
			return err
		}
	}
}

func (r *callRate) giveBack() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n := len(r.sent); n > 0 {
		r.sent = r.sent[:n-1]
	}
}

func (r *callRate) refused() {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := time.Now()
	if now.Sub(r.cut) >= rateCutEvery {
		r.per = max(leastPerSecond, r.per/2)
		r.cut = now
	}
	r.lastRefusal = now
}

func (p *pacer) rate(write bool) *callRate {
	if write {
		return p.writes
	}
	return p.reads
}
