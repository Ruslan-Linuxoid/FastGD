package drive

import (
	"context"
	"crypto/x509"
	"errors"
	"net"
	"net/url"
	"syscall"
	"testing"

	"google.golang.org/api/googleapi"
)

func TestWhatCountsAsTheNetwork(t *testing.T) {
	t.Parallel()
	for name, c := range map[string]struct {
		err  error
		want bool
	}{
		"refused connection": {&url.Error{Op: "Get", URL: "https://x", Err: &net.OpError{Op: "dial",
			Err: syscall.ECONNREFUSED}}, true},
		"failed lookup": {&url.Error{Op: "Get", URL: "https://x", Err: &net.DNSError{Err: "no such host",
			Name: "x"}}, true},
		"unknown certificate":         {&url.Error{Op: "Get", URL: "https://x", Err: x509.UnknownAuthorityError{}}, false},
		"certificate of another host": {&url.Error{Op: "Get", URL: "https://x", Err: x509.HostnameError{Host: "x"}}, false},
	} {
		if got := IsNetwork(c.err); got != c.want {
			t.Errorf("%s counts as the network: %v, want %v", name, got, c.want)
		}
	}
}

func TestDailyUploadLimitIsToldByItsWording(t *testing.T) {
	t.Parallel()
	refusal := func(message string) error {
		return &googleapi.Error{Code: 403, Errors: []googleapi.ErrorItem{{Reason: "userRateLimitExceeded", Message: message}}}
	}
	for message, limit := range map[string]bool{
		"User rate limit exceeded.": true,
		"User Rate Limit Exceeded.": false,
		"Rate Limit Exceeded":       false,
		"":                          false,
	} {
		v, _, err := judge(context.Background(), refusal(message), true)
		if got := errors.Is(err, ErrUploadLimit); got != limit {
			t.Errorf("%q on an upload is the daily limit: %v, want %v", message, got, limit)
		}
		if !limit && v != rateLimited {
			t.Errorf("%q on an upload got the verdict %v, want a refusal by the rate", message, v)
		}
		if _, _, err := judge(context.Background(), refusal(message), false); errors.Is(err, ErrUploadLimit) {
			t.Errorf("%q on a call that uploads nothing is taken for the daily upload limit", message)
		}
	}
}
