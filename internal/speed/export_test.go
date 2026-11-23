package speed

import "time"

func (m *Meter) BytesPerSecond() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.rate
}

func (m *Meter) Latency() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.latency
}
