package drive

import (
	"sync"
	"time"
)

type cost struct {
	units  int
	write  bool
	upload bool
}

var (
	costDownload   = cost{units: 200}
	costList       = cost{units: 100}
	costWrite      = cost{units: 50, write: true}
	costOther      = cost{units: 5}
	costOtherWrite = cost{units: 5, write: true}

	costUpload = cost{units: 50, write: true, upload: true}
	costChunk  = cost{units: 5, write: true, upload: true}
)

type Limits struct {
	Units       int
	UnitsWindow time.Duration

	RetryFor time.Duration

	PerSecond float64

	IdleRead time.Duration
}

const idleRead = 10 * time.Second

func DefaultLimits() Limits {

	return Limits{Units: 325_000, UnitsWindow: time.Minute, RetryFor: 15 * time.Second, IdleRead: idleRead}
}

type pacer struct {
	lim Limits

	mu     sync.Mutex
	spent  []*spend
	used   int
	budget int

	lastRefusal time.Time
	grown       time.Time
	offline     bool

	onReconnect  func()
	reconnecting int
	settled      *sync.Cond

	reads, writes *callRate
}

type spend struct {
	at    time.Time
	units int
}

const (
	growth      = 0.1
	calmWindows = 5

	cutTo = 0.8

	floorDivisor = 16
)

func newPacer(lim Limits) *pacer {
	if lim.Units == 0 {
		lim.Units = DefaultLimits().Units
	}
	if lim.UnitsWindow == 0 {
		lim.UnitsWindow = DefaultLimits().UnitsWindow
	}
	if lim.RetryFor == 0 {
		lim.RetryFor = DefaultLimits().RetryFor
	}
	if lim.IdleRead == 0 {
		lim.IdleRead = DefaultLimits().IdleRead
	}
	if lim.PerSecond == 0 {
		lim.PerSecond = perSecond
	}
	rate := func() *callRate { return &callRate{top: lim.PerSecond, per: lim.PerSecond} }
	p := &pacer{lim: lim, budget: lim.Units, reads: rate(), writes: rate()}
	p.settled = sync.NewCond(&p.mu)
	return p
}

func (p *pacer) expiry() time.Duration { return p.lim.UnitsWindow + p.lim.UnitsWindow/50 }

func (p *pacer) tryTake(units int) (*spend, time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	p.prune(now)
	if p.budget < p.lim.Units && now.Sub(p.lastRefusal) >= calmWindows*p.lim.UnitsWindow &&
		now.Sub(p.grown) >= p.lim.UnitsWindow {
		p.budget = min(p.lim.Units, p.budget+max(1, int(float64(p.budget)*growth)))
		p.grown = now
	}
	if p.used == 0 || p.used+units <= p.budget {
		s := &spend{now, units}
		p.spent = append(p.spent, s)
		p.used += units
		return s, 0
	}
	return nil, p.spent[0].at.Add(p.expiry()).Sub(now)
}

func (p *pacer) giveBack(s *spend) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.used -= s.units
	s.units = 0
}

func (p *pacer) prune(now time.Time) {
	i := 0
	for i < len(p.spent) && now.Sub(p.spent[i].at) >= p.expiry() {
		p.used -= p.spent[i].units

		p.spent[i].units = 0
		i++
	}
	p.spent = p.spent[i:]
}

func (p *pacer) note(v verdict) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	if v == rateLimited {
		if now.Sub(p.lastRefusal) >= p.lim.UnitsWindow/10 {
			p.prune(now)

			cut := max(int(float64(p.used)*cutTo), p.budget/2, p.lim.Units/floorDivisor, costDownload.units)
			p.budget = min(p.budget, cut)
		}
		p.lastRefusal = now
	}
	switch {
	case v == offline:
		p.offline = true
	case v.answered() && p.offline:
		p.offline = false
		if fn := p.onReconnect; fn != nil {
			p.reconnecting++
			go func() {
				fn()
				p.mu.Lock()
				p.reconnecting--
				p.settled.Broadcast()
				p.mu.Unlock()
			}()
		}
	}
}

func (p *pacer) isOffline() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.offline
}
