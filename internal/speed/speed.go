package speed

import (
	"context"
	"net/http/httptrace"
	"sync"
	"time"
)

const step = 1 << 20

type Meter struct {
	mu      sync.Mutex
	rate    float64
	latency time.Duration
	limit   float64
	active  int
	from    time.Time
	busy    time.Duration
	bytes   int64
}

const (
	sampleEvery = time.Second

	firstSample      = 100 * time.Millisecond
	firstSampleBytes = step

	minSampleTime  = 10 * time.Millisecond
	minSampleBytes = 64 << 10

	ewma = 0.3
)

func (m *Meter) Arrive(n int, first bool, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if first {
		m.active++
		if m.active == 1 {
			m.from = now
			return
		}
	}
	m.bytes += int64(n)
	m.sample(now)
}

func (m *Meter) Leave(now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.leave(now)
}

func (m *Meter) Answered(n int, now time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	end := now.Add(-m.latency)
	if end.After(m.from) {
		m.bytes += int64(n)
		m.sample(end)
	} else {
		end = m.from
	}
	m.leave(end)
}

func blend(v, s float64) float64 {
	if v == 0 {
		return s
	}
	return (1-ewma)*v + ewma*s
}

func (m *Meter) SetLimit(bytesPerSecond int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.limit = float64(bytesPerSecond)
}

func (m *Meter) Amount(d time.Duration) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return amount(m.capped(), d+m.latency)
}

func (m *Meter) Share(d time.Duration) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return amount(m.capped()/float64(m.active+1), d+m.latency)
}

func (m *Meter) Trace(ctx context.Context) context.Context {
	var mu sync.Mutex
	var wrote time.Time
	return httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		WroteRequest: func(i httptrace.WroteRequestInfo) {
			mu.Lock()
			defer mu.Unlock()
			wrote = time.Time{}
			if i.Err == nil {
				wrote = time.Now()
			}
		},
		GotFirstResponseByte: func() {
			mu.Lock()
			at := wrote
			wrote = time.Time{}
			mu.Unlock()
			if !at.IsZero() {
				m.waited(time.Since(at))
			}
		},
	})
}

func scaleDown(n int64) int64 {
	s := int64(step)
	for s*2 <= n {
		s *= 2
	}
	return s
}

func amount(rate float64, d time.Duration) int64 {
	if rate <= 0 {
		return step
	}
	return scaleDown(int64(rate * d.Seconds()))
}

func (m *Meter) capped() float64 {
	if m.limit > 0 {
		return min(m.rate, m.limit)
	}
	return m.rate
}

func (m *Meter) leave(now time.Time) {
	m.active--
	if m.active == 0 {
		m.busy += now.Sub(m.from)
	}
}

func (m *Meter) sample(now time.Time) {
	took := m.busy + now.Sub(m.from)
	if took < minSampleTime || m.bytes < minSampleBytes {
		return
	}
	first := m.rate == 0 && (took >= firstSample || m.bytes >= firstSampleBytes)
	if !first && took < sampleEvery {
		return
	}
	m.rate = blend(m.rate, float64(m.bytes)/took.Seconds())
	m.from, m.busy, m.bytes = now, 0, 0
}

func (m *Meter) waited(d time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.latency = time.Duration(blend(float64(m.latency), float64(d)))
}
