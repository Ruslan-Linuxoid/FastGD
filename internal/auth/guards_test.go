package auth

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"golang.org/x/oauth2"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
)

type login struct {
	tok *oauth2.Token
	err error
}

func startLogin(t *testing.T, cfg *oauth2.Config) (*url.URL, <-chan login) {
	t.Helper()
	out, w := io.Pipe()
	done := make(chan login, 1)
	go func() {
		tok, err := Login(t.Context(), cfg, "127.0.0.1:0", w, http.DefaultClient)
		done <- login{tok, err}
	}()
	return signInAddress(t, out), done
}

func comeBack(t *testing.T, address *url.URL, state, code string) int {
	t.Helper()
	back := address.Query().Get("redirect_uri") + "?" + url.Values{"state": {state}, "code": {code}}.Encode()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, back, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	return resp.StatusCode
}

func TestLoginTakesTheCodeOfItsOwnStateAlone(t *testing.T) {
	t.Parallel()
	var challenge atomic.Pointer[string]
	cfg := google(t, func(form url.Values) (int, string) { return codeAnswer(form, *challenge.Load()) })
	address, done := startLogin(t, cfg)
	c := address.Query().Get("code_challenge")
	challenge.Store(&c)
	for _, state := range []string{"", "someone-else"} {
		if code := comeBack(t, address, state, "stolen"); code != http.StatusBadRequest {
			t.Fatalf("a browser with the state %q came back to %d, want 400", state, code)
		}
	}
	if code := comeBack(t, address, address.Query().Get("state"), "c1"); code != http.StatusOK {
		t.Fatalf("the browser of the sign-in came back to %d", code)
	}
	if got := <-done; got.err != nil || got.tok.AccessToken != "a" {
		t.Fatalf("the sign-in gave %+v, %v", got.tok, got.err)
	}
}

func awayFrom(t *testing.T) (*oauth2.Config, *atomic.Int32) {
	t.Helper()
	var got atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { got.Add(1) }))
	t.Cleanup(other.Close)
	return google(t, func(url.Values) (int, string) { return http.StatusTemporaryRedirect, other.URL + "/token" }), &got
}

func TestARenewalRedirectedAwayGoesNowhere(t *testing.T) {
	t.Parallel()
	cfg, got := awayFrom(t)
	src, err := Source(t.Context(), cfg, expiredTokenFile(t), http.DefaultClient, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := src.Token(); err == nil {
		t.Fatal("a renewal redirected away passed")
	}
	if n := got.Load(); n != 0 {
		t.Fatalf("the other host got %d requests", n)
	}
}

func TestAnExchangeRedirectedAwayGoesNowhere(t *testing.T) {
	t.Parallel()
	cfg, got := awayFrom(t)
	address, done := startLogin(t, cfg)
	comeBack(t, address, address.Query().Get("state"), "c1")
	if r := <-done; r.err == nil {
		t.Fatal("an exchange redirected away passed")
	}
	if n := got.Load(); n != 0 {
		t.Fatalf("the other host got %d requests", n)
	}
}

func TestARenewalRedirectedAwayIsNotTheNetwork(t *testing.T) {
	t.Parallel()
	cfg, _ := awayFrom(t)
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
	if err == nil || drive.IsNetwork(err) {
		t.Fatalf("a renewal redirected away ends with %v, the network %v", err, drive.IsNetwork(err))
	}
}
