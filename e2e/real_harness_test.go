//go:build e2e && realdrive

package e2e

import (
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/config"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

const realReady = 5 * time.Minute

func newRealDaemon(t *testing.T, r *testenv.Real, p *proxy) *daemon {
	t.Helper()
	dir := t.TempDir()
	clientID, token := filepath.Join(dir, "client_id.json"), filepath.Join(dir, "token.json")
	for from, to := range map[string]string{"client_id.json": clientID, "token.json": token} {
		data, err := os.ReadFile(filepath.Join(r.Dir, from))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(to, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	d := newDaemonIn(t, nil, dir, clientID, token)
	a, err := r.Drive.Account(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	d.mnt = config.MountFolder(dir, a.Email)
	d.readyIn = realReady
	d.env = []string{"HTTPS_PROXY=" + p.url, "NO_PROXY="}
	return d
}

func inFolder(d *daemon, r *testenv.Real, name string) string {
	return d.path(filepath.Join(r.FolderName, name))
}

type proxy struct {
	url string
	mu  sync.Mutex

	down  bool
	conns map[net.Conn]bool
}

func startProxy(t *testing.T) *proxy {
	t.Helper()
	ln, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	p := &proxy{url: "http://" + ln.Addr().String(), conns: map[net.Conn]bool{}}
	srv := &http.Server{Handler: p, ReadHeaderTimeout: time.Minute}
	go srv.Serve(ln)
	t.Cleanup(func() {
		srv.Close()
		p.setDown(true)
	})
	return p
}

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	u := url.URL{Scheme: "https", Host: r.Host}
	if r.Method != http.MethodConnect || !isGoogle(u.String()) {
		http.Error(w, "only a CONNECT to Google", http.StatusForbidden)
		return
	}
	up, err := (&net.Dialer{Timeout: 15 * time.Second}).DialContext(r.Context(), "tcp", r.Host)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	hj, ok := w.(http.Hijacker)
	if !ok {
		up.Close()
		http.Error(w, "no hijack", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	down, _, err := hj.Hijack()
	if err != nil {
		up.Close()
		return
	}
	if !p.track(up) {
		down.Close()
		return
	}
	if !p.track(down) {
		p.untrack(up)
		return
	}
	go func() {
		defer p.untrack(up)
		defer p.untrack(down)
		go io.Copy(up, down)
		io.Copy(down, up)
	}()
}

func (p *proxy) setDown(down bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.down = down
	if down {
		for c := range p.conns {
			c.Close()
		}
	}
}

func (p *proxy) track(c net.Conn) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.down {
		c.Close()
		return false
	}
	p.conns[c] = true
	return true
}

func (p *proxy) untrack(c net.Conn) {
	p.mu.Lock()
	defer p.mu.Unlock()
	delete(p.conns, c)
	c.Close()
}

func isGoogle(address string) bool {
	u, err := url.Parse(address)
	if err != nil {
		return false
	}
	host := "." + u.Hostname()
	for _, s := range []string{".googleapis.com", ".google.com", ".googleusercontent.com"} {
		if len(host) > len(s) && host[len(host)-len(s):] == s {
			return true
		}
	}
	return false
}

func inDrive(t *testing.T, r *testenv.Real, name, sum string) {
	t.Helper()
	waitFor(t, name+" in Drive with its md5", 2*time.Minute, func() bool {
		var list struct {
			Files []struct {
				ID, Name    string
				Md5Checksum string `json:"md5Checksum"`
			}
		}
		q := url.Values{"q": {"'" + r.Folder + "' in parents and name = '" + name + "' and trashed = false"},
			"fields": {"files(id,name,md5Checksum)"}}
		if code, body := r.GetJSON("files", q, &list); code != http.StatusOK {
			t.Fatalf("files.list answered %d: %s", code, body)
		}
		for _, f := range list.Files {
			if f.Md5Checksum == sum {
				return true
			}
		}
		time.Sleep(3 * time.Second)
		return false
	})
}
