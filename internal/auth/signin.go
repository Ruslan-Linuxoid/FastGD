package auth

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"golang.org/x/oauth2"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
)

var errRenewalRefused = errors.New("auth: Drive refuses the renewed token as well")

type signin struct {
	out error

	outRefresh string

	email string

	other string

	forced     bool
	afterForce string
}

func (s *saving) signOut(err error) error {
	if s.out == nil {
		s.out = fmt.Errorf("%w: %w", drive.ErrLoginExpired, err)
		s.outRefresh = s.written.RefreshToken
		s.email, _ = ReadEmail(s.path)
	}
	return s.out
}

func (s *saving) signIn() error {
	if s.out == nil {
		return nil
	}
	t, err := ReadToken(s.path)
	if err != nil || t.RefreshToken == s.outRefresh {
		return s.out
	}
	if email, _ := ReadEmail(s.path); s.email != "" && email != s.email {
		if s.other != t.RefreshToken {
			s.other = t.RefreshToken
			s.log.Error("the new sign-in is not taken: it is of another account than the mount, which is named by "+
				"its account; sign in as that account, or move the cache and mount_in aside first",
				"sign-in", email, "mount", s.email)
		}
		return fmt.Errorf("%w: the sign-in is of %q, and the mount is of %q", drive.ErrLoginExpired, email, s.email)
	}
	s.base = s.cfg.TokenSource(s.ctx, t)
	s.written = *t
	s.signin = signin{}
	s.log.Info("a new sign-in is taken from the token file")
	return nil
}

func (s *saving) renewed(t *oauth2.Token) {
	if s.forced {
		s.forced, s.afterForce = false, t.AccessToken
	}
}

func (s *saving) expire(access string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case s.out != nil || access == "":
	case access == s.afterForce:
		_ = s.signOut(errRenewalRefused)
	case access == s.written.AccessToken && !s.forced:
		s.base = s.cfg.TokenSource(s.ctx, &oauth2.Token{RefreshToken: s.written.RefreshToken})
		s.forced = true
	}
}

func (s *saving) took(access string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if access == s.afterForce {
		s.afterForce = ""
	}
}

type refusals struct {
	base http.RoundTripper
	src  *saving
}

func (t refusals) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := t.base.RoundTrip(r)
	if err != nil {
		return resp, err
	}
	access := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	switch {
	case resp.StatusCode == http.StatusUnauthorized || authError(resp):
		t.src.expire(access)
	case resp.StatusCode < http.StatusInternalServerError:

		t.src.took(access)
	}
	return resp, err
}

const maxAuthErrorBody = 64 << 10

func authError(resp *http.Response) bool {
	if resp.StatusCode != http.StatusForbidden || resp.Body == nil {
		return false
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxAuthErrorBody))
	resp.Body = struct {
		io.Reader
		io.Closer
	}{io.MultiReader(bytes.NewReader(b), resp.Body), resp.Body}
	return err == nil && bytes.Contains(b, []byte(`"authError"`))
}
