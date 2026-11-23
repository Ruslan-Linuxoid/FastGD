package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/oauth2"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
)

var ErrNoToken = errors.New("auth: the token file holds no refresh token: run fastgd auth")

func ReadToken(path string) (*oauth2.Token, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("auth: token_file: %w", err)
	}
	var t oauth2.Token
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("auth: token_file %s: %w", path, err)
	}
	if t.RefreshToken == "" {
		return nil, ErrNoToken
	}
	return &t, nil
}

func SaveToken(path string, t *oauth2.Token) error {
	data, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	return nil
}

func Source(
	ctx context.Context, cfg *oauth2.Config, path string, hc *http.Client, log *slog.Logger,
) (oauth2.TokenSource, error) {
	return source(ctx, cfg, path, hc, log)
}

func source(ctx context.Context, cfg *oauth2.Config, path string, hc *http.Client, log *slog.Logger) (*saving, error) {
	t, err := ReadToken(path)
	if err != nil {
		return nil, err
	}
	ctx = context.WithValue(ctx, oauth2.HTTPClient, onItsHost(hc))
	return &saving{cfg: cfg, ctx: ctx, base: cfg.TokenSource(ctx, t), path: path, written: *t, log: log}, nil
}

type saving struct {
	signin

	cfg  *oauth2.Config
	ctx  context.Context
	path string
	log  *slog.Logger

	mu   sync.Mutex
	base oauth2.TokenSource

	written oauth2.Token
}

func (s *saving) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	err := s.signIn()
	base := s.base
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	t, err := base.Token()
	s.mu.Lock()
	defer s.mu.Unlock()

	if base != s.base {
		if err != nil {
			return nil, fmt.Errorf("auth: renew the token of the sign-in before: %w", err)
		}
		return t, nil
	}
	if err != nil {
		if refused(err) {
			return nil, s.signOut(err)
		}
		return nil, fmt.Errorf("auth: renew the token: %w", err)
	}
	s.renewed(t)
	if t.AccessToken == s.written.AccessToken && t.RefreshToken == s.written.RefreshToken {
		return t, nil
	}

	s.written = *t
	if err := SaveToken(s.path, t); err != nil {
		s.log.Error("the renewed token is not saved", "err", err)
	}
	return t, nil
}

func refused(err error) bool {
	re, ok := errors.AsType[*oauth2.RetrieveError](err)
	if !ok || re.Response == nil {
		return false
	}
	return re.Response.StatusCode == http.StatusBadRequest || re.Response.StatusCode == http.StatusUnauthorized
}

func HTTPClient(
	ctx context.Context, clientIDFile, tokenFile string, base http.RoundTripper, log *slog.Logger,
) (*http.Client, error) {
	cfg, err := Client(clientIDFile)
	if err != nil {
		return nil, err
	}
	src, err := source(ctx, cfg, tokenFile, &http.Client{Transport: base}, log)
	if err != nil {
		return nil, err
	}
	return &http.Client{Transport: &oauth2.Transport{Source: src, Base: refusals{base, src}}}, nil
}

var errRedirectAway = fmt.Errorf("auth: %w: another host than the sign-in's", drive.ErrRedirectAway)

func onItsHost(hc *http.Client) *http.Client {
	c := *hc
	next := c.CheckRedirect
	c.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Scheme != via[0].URL.Scheme || req.URL.Host != via[0].URL.Host {
			return errRedirectAway
		}
		if next != nil {
			return next(req, via)
		}
		if len(via) >= maxRedirects {
			return errors.New("auth: stopped after 10 redirects")
		}
		return nil
	}
	return &c
}

const maxRedirects = 10
