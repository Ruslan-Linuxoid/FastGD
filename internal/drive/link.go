package drive

import (
	"net"
	"net/http"
	"time"
)

func (c *Client) OnReconnect(fn func()) {
	p := c.pace
	p.mu.Lock()
	defer p.mu.Unlock()
	p.onReconnect = fn
	for p.reconnecting > 0 {
		p.settled.Wait()
	}
}

func (c *Client) Offline() bool { return c.pace.isOffline() }

const attemptTimeout = 15 * time.Second

func Transport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()

	const part = attemptTimeout / 3

	const keepAlive = 30 * time.Second
	t.DialContext = (&net.Dialer{Timeout: part, KeepAlive: keepAlive}).DialContext
	t.TLSHandshakeTimeout = part
	t.ResponseHeaderTimeout = part
	return t
}
