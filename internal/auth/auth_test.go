package auth

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
)

func google(t *testing.T, answer func(form url.Values) (int, string)) *oauth2.Config {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		code, body := answer(r.PostForm)
		if code == http.StatusTemporaryRedirect {
			w.Header().Set("Location", body)
			body = ""
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		fmt.Fprint(w, body)
	}))
	t.Cleanup(srv.Close)
	path := filepath.Join(t.TempDir(), "client_id.json")
	file := fmt.Sprintf(`{"installed":{"client_id":"id","client_secret":"secret","auth_uri":%q,"token_uri":%q}}`,
		srv.URL+"/auth", srv.URL+"/token")
	if err := os.WriteFile(path, []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Client(path)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func expiredTokenFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "token.json")
	if err := SaveToken(path, &oauth2.Token{AccessToken: "old", RefreshToken: "r1", Expiry: time.Unix(1, 0)}); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestARenewedTokenIsSavedAndTheFileIsReadOnce(t *testing.T) {
	t.Parallel()
	cfg := google(t, func(form url.Values) (int, string) {
		if form.Get("refresh_token") != "r1" {
			return http.StatusBadRequest, `{"error":"invalid_grant"}`
		}
		return http.StatusOK, `{"access_token":"new","token_type":"Bearer","expires_in":3600}`
	})
	path := expiredTokenFile(t)
	src, err := Source(t.Context(), cfg, path, http.DefaultClient, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if tok, err := src.Token(); err != nil || tok.AccessToken != "new" {
		t.Fatalf("the first call has the token %+v, %v, want the renewed one", tok, err)
	}
	requireSaved(t, path)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if tok, err := src.Token(); err != nil || tok.AccessToken != "new" {
		t.Fatalf("a call after the token file is gone has %+v, %v", tok, err)
	}
}

func requireSaved(t *testing.T, path string) {
	t.Helper()
	saved, err := ReadToken(path)
	if err != nil || saved.AccessToken != "new" || saved.RefreshToken != "r1" {
		t.Fatalf("the token file holds %+v, %v, want the renewed token with the refresh token", saved, err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := fi.Mode().Perm(); mode != 0o600 {
		t.Fatalf("the token file has the rights %o, want the owner's alone", mode)
	}
	if left, _ := filepath.Glob(path + ".*"); len(left) != 0 {
		t.Fatalf("the save left %q", left)
	}
}

func TestARefusedRenewalIsAnExpiredLogin(t *testing.T) {
	t.Parallel()
	cfg := google(t, func(url.Values) (int, string) {
		return http.StatusBadRequest, `{"error":"invalid_grant","error_description":"Token has been expired or revoked."}`
	})
	src, err := Source(t.Context(), cfg, expiredTokenFile(t), http.DefaultClient, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://127.0.0.1:1/", http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := (&http.Client{Transport: &oauth2.Transport{Source: src}}).Do(req)
	if err == nil {
		resp.Body.Close()
	}
	if !drive.IsLoginExpired(err) || drive.IsNetwork(err) {
		t.Fatalf("a call after a refused renewal ends with %v: an expired login %v, the network %v",
			err, drive.IsLoginExpired(err), drive.IsNetwork(err))
	}
}

func TestATokenNotSavedIsToldOnce(t *testing.T) {
	t.Parallel()
	cfg := google(t, func(url.Values) (int, string) {
		return http.StatusOK, `{"access_token":"new","token_type":"Bearer","expires_in":3600}`
	})
	path := expiredTokenFile(t)
	var log strings.Builder
	src, err := Source(t.Context(), cfg, path, http.DefaultClient, slog.New(slog.NewTextHandler(&log, nil)))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(path), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
			t.Error(err)
		}
	})
	for range 3 {
		if tok, err := src.Token(); err != nil || tok.AccessToken != "new" {
			t.Fatalf("a call has the token %+v, %v, want the renewed one", tok, err)
		}
	}
	if n := strings.Count(log.String(), "the renewed token is not saved"); n != 1 {
		t.Fatalf("the failed save is told %d times, want once:\n%s", n, log.String())
	}
}

func TestATokenFileWithoutARefreshTokenIsRefused(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "token.json")
	if err := os.WriteFile(path, []byte(`{"access_token":"a"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadToken(path); !errors.Is(err, ErrNoToken) {
		t.Fatalf("a token file without a refresh token gives %v", err)
	}
}

func TestAClientOfTheWebIsRefused(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "client_id.json")
	if err := os.WriteFile(path, []byte(`{"web":{"client_id":"id"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Client(path); err == nil || !strings.Contains(err.Error(), "Desktop app") {
		t.Fatalf("a client of the type Web application gives %v", err)
	}
}

func TestLoginGivesTheTokenOfTheCode(t *testing.T) {
	t.Parallel()
	var challenge atomic.Pointer[string]
	cfg := google(t, func(form url.Values) (int, string) { return codeAnswer(form, *challenge.Load()) })
	address, done := startLogin(t, cfg)
	q := address.Query()
	c := q.Get("code_challenge")
	challenge.Store(&c)
	if q.Get("access_type") != "offline" || q.Get("scope") != Scope || c == "" {
		t.Fatalf("the address of the sign-in is %s", address)
	}
	if code := comeBack(t, address, q.Get("state"), "c1"); code != http.StatusOK {
		t.Fatalf("the browser came back to %d", code)
	}
	got := <-done
	if got.err != nil || got.tok.AccessToken != "a" || got.tok.RefreshToken != "r" {
		t.Fatalf("the sign-in gave %+v, %v", got.tok, got.err)
	}
}

func codeAnswer(form url.Values, challenge string) (int, string) {
	v := form.Get("code_verifier")
	if form.Get("code") != "c1" || v == "" || oauth2.S256ChallengeFromVerifier(v) != challenge {
		return http.StatusBadRequest, `{"error":"invalid_grant"}`
	}
	return http.StatusOK, `{"access_token":"a","refresh_token":"r","token_type":"Bearer","expires_in":3600}`
}

func signInAddress(t *testing.T, out io.Reader) *url.URL {
	t.Helper()
	sc := bufio.NewScanner(out)
	for sc.Scan() {
		if strings.HasPrefix(sc.Text(), "http") {
			u, err := url.Parse(sc.Text())
			if err != nil {
				t.Fatal(err)
			}
			go io.Copy(io.Discard, out)
			return u
		}
	}
	t.Fatal("Login printed no address")
	return nil
}
