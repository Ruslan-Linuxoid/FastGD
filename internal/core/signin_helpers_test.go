package core_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"golang.org/x/oauth2"

	"github.com/Ruslan-Linuxoid/FastGD/internal/auth"
	"github.com/Ruslan-Linuxoid/FastGD/internal/clock"
	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

type google struct {
	drive http.RoundTripper

	mu       sync.Mutex
	refresh  map[string]bool
	access   map[string]bool
	n        int
	requests int

	refuses bool

	forbids bool
}

func (g *google) RoundTrip(r *http.Request) (*http.Response, error) {
	g.mu.Lock()
	g.requests++
	g.mu.Unlock()
	if r.URL.Host == "oauth.test" {
		return g.token(r)
	}
	g.mu.Lock()
	ok := g.access[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")] && !g.refuses
	forbids := g.forbids
	g.mu.Unlock()
	if !ok {
		if r.Body != nil {
			_, _ = io.Copy(io.Discard, r.Body)
			_ = r.Body.Close()
		}
		if forbids {
			return reply(r, http.StatusForbidden,
				`{"error":{"code":403,"message":"Invalid Credentials","errors":[{"reason":"authError"}]}}`), nil
		}
		return reply(r, http.StatusUnauthorized,
			`{"error":{"code":401,"message":"Invalid Credentials","errors":[{"reason":"authError"}]}}`), nil
	}
	return g.drive.RoundTrip(r)
}

func (g *google) token(r *http.Request) (*http.Response, error) {
	_ = r.ParseForm()
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.refresh[r.Form.Get("refresh_token")] {
		return reply(r, http.StatusBadRequest,
			`{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`), nil
	}
	g.n++
	access := fmt.Sprintf("a%d", g.n)
	g.access[access] = true
	return reply(r, http.StatusOK, `{"access_token":"`+access+`","token_type":"Bearer","expires_in":3600}`), nil
}

func (g *google) revoke() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.refresh, g.access = map[string]bool{}, map[string]bool{}
}

func (g *google) count() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	n := g.requests
	g.requests = 0
	return n
}

func reply(r *http.Request, code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Status: http.StatusText(code), Proto: "HTTP/1.1", ProtoMajor: 1,
		ProtoMinor: 1, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

type records struct {
	mu   sync.Mutex
	msgs []string
}

func (h *records) Enabled(_ context.Context, l slog.Level) bool { return l >= slog.LevelWarn }
func (h *records) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.msgs = append(h.msgs, r.Level.String()+" "+r.Message)
	return nil
}
func (h *records) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *records) WithGroup(string) slog.Handler      { return h }

func (h *records) take() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	m := h.msgs
	h.msgs = nil
	return m
}

type signedIn struct {
	g         *google
	emu       *emulator.Emulator
	fs        *core.FS
	log       *records
	tokenFile string
}

func mountSignedIn(t *testing.T, test func(m *signedIn)) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		dir := t.TempDir()
		emu := emulator.New()
		m := &signedIn{emu: emu, log: &records{}, tokenFile: filepath.Join(dir, "token.json"),
			g: &google{drive: emulator.InProcess(emu), refresh: map[string]bool{"r1": true}, access: map[string]bool{}}}
		clientFile := filepath.Join(dir, "client.json")
		if err := os.WriteFile(clientFile, []byte(`{"installed":{"client_id":"c","client_secret":"s",`+
			`"auth_uri":"http://oauth.test/auth","token_uri":"http://oauth.test/token"}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		m.signIn(t, "r1", "you@example.com")
		ctx, cancel := context.WithCancel(context.Background())
		log := slog.New(m.log)
		hc, err := auth.HTTPClient(ctx, clientFile, m.tokenFile, m.g, log)
		if err != nil {
			t.Fatal(err)
		}
		cl, err := drive.New(ctx, drive.Options{BaseURL: "http://emulator", HTTPClient: hc})
		if err != nil {
			t.Fatal(err)
		}
		m.fs, err = core.Open(ctx, core.Options{CacheDir: filepath.Join(dir, "cache"), Drive: cl,
			Clock: clock.Real{}, UID: 1, GID: 1, FileMode: 0o644, DirMode: 0o755, Log: log, CacheSize: 1 << 30,
			TransferTime: 10 * time.Second, PrefetchShare: 0.5})
		if err != nil {
			t.Fatal(err)
		}
		var runs sync.WaitGroup
		runs.Go(func() { m.fs.Run(ctx) })
		time.Sleep(time.Minute)
		test(m)
		cancel()
		runs.Wait()
		if err := m.fs.Close(); err != nil {
			t.Error(err)
		}
	})
}

func (m *signedIn) signIn(t *testing.T, refresh, email string) {
	t.Helper()
	if err := auth.SaveEmail(m.tokenFile, email); err != nil {
		t.Fatal(err)
	}
	if err := auth.SaveToken(m.tokenFile, &oauth2.Token{RefreshToken: refresh, AccessToken: "x",
		Expiry: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
}

func (m *signedIn) revoked(t *testing.T) []string {
	t.Helper()
	m.log.take()
	m.g.revoke()
	m.write(t, "new.txt")
	time.Sleep(time.Minute)
	if _, needed, _ := m.fs.Store().GetState(meta.StateLoginNeeded); !needed {
		t.Fatal("a minute after the sign-in was withdrawn status does not tell it")
	}
	return m.log.take()
}

func (m *signedIn) write(t *testing.T, name string) {
	t.Helper()
	_, fh, err := m.fs.Create(uint64(meta.RootIno), name, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.fs.Write(context.Background(), fh, 0, []byte(name)); err != nil {
		t.Fatal(err)
	}
	m.fs.Release(fh)
}

func (m *signedIn) uploaded(name string) bool {
	for _, f := range m.emu.Children(m.emu.Root()) {
		if f.Name == name {
			return true
		}
	}
	return false
}
