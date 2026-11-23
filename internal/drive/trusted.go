package drive

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

var ErrRedirectAway = errors.New("drive: an answer redirects to an address the request may not go to")

var errRedirectNotOfDrive = fmt.Errorf("%w: not one of Drive", ErrRedirectAway)

const maxRedirects = 10

func (c *Client) redirectsToDrive(
	next func(*http.Request, []*http.Request) error,
) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if !c.trusted(req.URL.String()) {
			return errRedirectNotOfDrive
		}
		if next != nil {
			return next(req, via)
		}
		if len(via) >= maxRedirects {
			return errors.New("drive: stopped after 10 redirects")
		}
		return nil
	}
}

func (c *Client) trusted(address string) bool {
	u, err := url.Parse(address)
	if err != nil || u.Host == "" {
		return false
	}
	if base, err := url.Parse(c.uploadBase); err == nil && u.Scheme == base.Scheme && u.Host == base.Host {
		return true
	}
	if u.Scheme != "https" {
		return false
	}
	host := "." + u.Hostname()
	for _, suffix := range []string{".googleapis.com", ".google.com", ".googleusercontent.com"} {
		if strings.HasSuffix(host, suffix) {
			return true
		}
	}
	return false
}
