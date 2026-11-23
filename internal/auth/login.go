package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"golang.org/x/oauth2"
)

const loginTimeout = 10 * time.Minute

func Login(
	ctx context.Context, cfg *oauth2.Config, listen string, out io.Writer, hc *http.Client,
) (*oauth2.Token, error) {
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", listen)
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	defer ln.Close()
	c := *cfg

	c.RedirectURL = fmt.Sprintf("http://127.0.0.1:%d/", ln.Addr().(*net.TCPAddr).Port)
	state := rand.Text()
	verifier := oauth2.GenerateVerifier()
	got := make(chan result, 1)
	srv := &http.Server{Handler: redirect{state: state, got: got}, ReadHeaderTimeout: time.Minute}
	go srv.Serve(ln)
	defer srv.Close()
	fmt.Fprintf(out, "open this address in a browser and sign in with the Google account of the Drive:\n\n%s\n\n",
		c.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce, oauth2.S256ChallengeOption(verifier)))
	ctx, cancel := context.WithTimeout(ctx, loginTimeout)
	defer cancel()
	var r result
	select {
	case r = <-got:
	case <-ctx.Done():
		return nil, fmt.Errorf("auth: no sign-in came: %w", ctx.Err())
	}
	if r.err != nil {
		return nil, r.err
	}
	t, err := c.Exchange(context.WithValue(ctx, oauth2.HTTPClient, onItsHost(hc)), r.code, oauth2.VerifierOption(verifier))
	if err != nil {
		return nil, fmt.Errorf("auth: exchange the code for a token: %w", err)
	}
	if t.RefreshToken == "" {
		return nil, errors.New("auth: Google gave no refresh token")
	}
	return t, nil
}

type result struct {
	code string
	err  error
}

type redirect struct {
	state string
	got   chan<- result
}

func (h redirect) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("state") != h.state {
		http.Error(w, "this is not the answer to the sign-in under way", http.StatusBadRequest)
		return
	}
	res := result{code: q.Get("code")}
	if e := q.Get("error"); e != "" || res.code == "" {
		res = result{err: fmt.Errorf("auth: the sign-in failed: %q", e)}
	}
	select {
	case h.got <- res:
	default:
	}
	if res.err != nil {
		http.Error(w, "the sign-in failed, see the terminal", http.StatusBadRequest)
		return
	}
	fmt.Fprintln(w, "the sign-in came back: the terminal tells how it ends, and the window can be closed")
}
