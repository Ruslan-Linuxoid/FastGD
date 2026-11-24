package auth

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/oauth2"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
)

type answer struct {
	code int
	body string
}

type scripted struct {
	mu       sync.Mutex
	script   []answer
	renewals int
}

func (g *scripted) RoundTrip(r *http.Request) (*http.Response, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if r.URL.Host == "oauth.test" {
		g.renewals++
		return reply(r, http.StatusOK, fmt.Sprintf(`{"access_token":"a%d","token_type":"Bearer","expires_in":3600}`,
			g.renewals)), nil
	}
	a := g.script[0]
	g.script = g.script[1:]
	return reply(r, a.code, a.body), nil
}

func reply(r *http.Request, code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Status: http.StatusText(code), Proto: "HTTP/1.1", ProtoMajor: 1,
		ProtoMinor: 1, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(body)), Request: r}
}

func drive403(why string) string {
	return `{"error":{"code":403,"message":"refused","errors":[{"reason":"` + why + `"}]}}`
}

func clientOver(t *testing.T, g *scripted) (*http.Client, *saving) {
	t.Helper()
	dir := t.TempDir()
	client := filepath.Join(dir, "client.json")
	if err := os.WriteFile(client, []byte(`{"installed":{"client_id":"c","client_secret":"s",`+
		`"auth_uri":"http://oauth.test/auth","token_uri":"http://oauth.test/token"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	token := filepath.Join(dir, "token.json")
	if err := SaveToken(token, &oauth2.Token{RefreshToken: "r1", Expiry: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}
	hc, err := HTTPClient(t.Context(), client, token, g, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	src, ok := hc.Transport.(*oauth2.Transport).Source.(*saving)
	if !ok {
		t.Fatal("the client takes its tokens from no saving")
	}
	return hc, src
}

func call(t *testing.T, hc *http.Client) (int, string, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://drive.test/drive/v3/about", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(b), nil
}

func TestA403OfAnotherReasonKeepsTheToken(t *testing.T) {
	t.Parallel()
	g := &scripted{script: []answer{{403, drive403("userRateLimitExceeded")},
		{403, drive403("insufficientFilePermissions")}}}
	hc, src := clientOver(t, g)
	for _, why := range []string{"userRateLimitExceeded", "insufficientFilePermissions"} {
		if code, body, err := call(t, hc); err != nil || code != 403 || body != drive403(why) {
			t.Fatalf("the 403 %s reached the caller as %d %q, %v", why, code, body, err)
		}
	}
	src.mu.Lock()
	defer src.mu.Unlock()
	if g.renewals != 1 || src.forced || src.afterForce != "" || src.out != nil {
		t.Errorf("after two 403 of other reasons: %d renewals, forced %v, afterForce %q, out %v; want the first "+
			"renewal alone", g.renewals, src.forced, src.afterForce, src.out)
	}
}

func TestA403AuthErrorThrowsTheTokenAway(t *testing.T) {
	t.Parallel()
	g := &scripted{script: []answer{{403, drive403("authError")}, {200, `{}`}}}
	hc, _ := clientOver(t, g)
	if code, body, err := call(t, hc); err != nil || code != 403 || body != drive403("authError") {
		t.Fatalf("the 403 authError reached the caller as %d %q, %v", code, body, err)
	}
	if code, _, err := call(t, hc); err != nil || code != 200 || g.renewals != 2 {
		t.Fatalf("the call after the 403 authError: %d, %v, %d renewals; want 200 on a renewed token", code, err,
			g.renewals)
	}
}

func TestA5xxLeavesTheRenewalForThe401Unsettled(t *testing.T) {
	t.Parallel()
	g := &scripted{script: []answer{{401, ""}, {503, ""}, {401, ""}}}
	hc, _ := clientOver(t, g)
	for _, want := range []int{401, 503, 401} {
		if code, _, err := call(t, hc); err != nil || code != want {
			t.Fatalf("a call got %d, %v; want %d", code, err, want)
		}
	}
	if _, _, err := call(t, hc); !errors.Is(err, drive.ErrLoginExpired) {
		t.Errorf("after a 401 to the token of the renewal for a 401 the call gives %v, want a refusal", err)
	}
	if g.renewals != 2 || len(g.script) != 0 {
		t.Errorf("%d renewals and %d answers left, want 2 renewals and every answer given", g.renewals, len(g.script))
	}
}
