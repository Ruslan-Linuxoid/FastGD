package drive

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"google.golang.org/api/googleapi"
)

var (
	ErrUploadLimit = errors.New("drive: the daily upload limit is reached")

	ErrStorageFull = errors.New("drive: the Drive storage is full")

	ErrDownloadQuota = errors.New("drive: the download quota is exceeded")

	ErrAbusive = errors.New("drive: the file is flagged as abusive and Drive gives its content to its owner alone")
)

const uploadLimitMessage = "User rate limit exceeded."

type verdict int

const (
	passed verdict = iota
	rateLimited
	transient
	offline
	refused
	unknown
)

func (v verdict) busy() bool { return v == rateLimited || v == transient }

func (v verdict) answered() bool { return v != offline && v != unknown }

func IsBusy(err error) bool {
	var gerr *googleapi.Error
	if !errors.As(err, &gerr) {
		return false
	}
	if gerr.Code == http.StatusInternalServerError {
		for _, e := range gerr.Errors {
			if e.Reason == "responseTooLarge" {
				return false
			}
		}
	}
	if errors.Is(err, ErrUploadLimit) {
		return false
	}
	v, _, _ := judged(gerr, false)
	return v.busy()
}

func Passing(err error) bool {
	v, _, _ := judged(err, false)
	return v == offline || v.busy()
}

type RetryLaterError struct {
	After time.Duration
	Err   error
}

func (e *RetryLaterError) Error() string {
	return fmt.Sprintf("%v (Drive asks to wait %v)", e.Err, e.After)
}

func (e *RetryLaterError) Unwrap() error { return e.Err }

func WaitAsked(err error) time.Duration {
	if rl, ok := errors.AsType[*RetryLaterError](err); ok {
		return rl.After
	}
	return 0
}

var ErrLoginExpired = errors.New("drive: the sign-in has expired")

func IsLoginExpired(err error) bool {
	if errors.Is(err, ErrLoginExpired) {
		return true
	}
	var gerr *googleapi.Error
	if !errors.As(err, &gerr) {
		return false
	}
	return gerr.Code == http.StatusUnauthorized || gerr.Code == http.StatusForbidden && hasReason(gerr, "authError")
}

func IsDenied(err error) bool {
	if errors.Is(err, ErrSessionGone) || IsLoginExpired(err) {
		return false
	}
	var gerr *googleapi.Error
	if !errors.As(err, &gerr) {
		return errors.Is(err, ErrNotFound)
	}
	switch gerr.Code {
	case http.StatusNotFound:
		return true
	case http.StatusForbidden:
		return !overLimit(err, gerr)
	}
	return false
}

func overLimit(err error, gerr *googleapi.Error) bool {
	if errors.Is(err, ErrStorageFull) || errors.Is(err, ErrUploadLimit) || errors.Is(err, ErrDownloadQuota) {
		return true
	}
	return slices.ContainsFunc(gerr.Errors, func(e googleapi.ErrorItem) bool {
		return strings.Contains(e.Reason, "LimitExceeded") || strings.Contains(e.Reason, "quota") ||
			strings.Contains(e.Reason, "Quota")
	})
}

func IsPrecondition(err error) bool { return hasCode(err, http.StatusPreconditionFailed) }

func IsConflict(err error) bool {
	return hasCode(err, http.StatusConflict) || errors.Is(err, ErrExists)
}

func hasCode(err error, code int) bool {
	var gerr *googleapi.Error
	return errors.As(err, &gerr) && gerr.Code == code
}

func hasReason(gerr *googleapi.Error, reason string) bool {
	return slices.ContainsFunc(gerr.Errors, func(e googleapi.ErrorItem) bool { return e.Reason == reason })
}

func IsNetwork(err error) bool {
	if _, ok := errors.AsType[*googleapi.Error](err); ok || refusedOnTheWay(err) {
		return false
	}
	if isCertificate(err) {
		return false
	}
	var nerr net.Error
	return errors.As(err, &nerr) || errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) ||
		errors.Is(err, syscall.ECONNRESET) || errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, errCut) ||
		errors.Is(err, errStalled)
}

func refusedOnTheWay(err error) bool {
	return errors.Is(err, ErrLoginExpired) || errors.Is(err, ErrRedirectAway)
}

func isCertificate(err error) bool {
	var cert *tls.CertificateVerificationError
	var unknown x509.UnknownAuthorityError
	var host x509.HostnameError
	var invalid x509.CertificateInvalidError
	return errors.As(err, &cert) || errors.As(err, &unknown) || errors.As(err, &host) || errors.As(err, &invalid)
}

func retryAfter(h http.Header) time.Duration {
	s, err := strconv.Atoi(h.Get("Retry-After"))
	if err != nil || s < 0 {
		return 0
	}
	return time.Duration(s) * time.Second
}

func IsRefusal(err error) bool {
	var gerr *googleapi.Error
	if !errors.As(err, &gerr) || gerr.Code < 400 || gerr.Code >= 500 {
		return false
	}
	switch gerr.Code {
	case http.StatusUnauthorized, http.StatusRequestTimeout, http.StatusTooManyRequests, http.StatusNotFound:

		return false
	}
	if errors.Is(err, ErrSessionGone) || overLimit(err, gerr) {
		return false
	}
	for _, e := range gerr.Errors {

		if slices.Contains([]string{"insufficientPermissions", "appNotAuthorizedToFile", "domainPolicy", "authError"},
			e.Reason) {
			return false
		}
	}
	return true
}
