package drive

import (
	"context"
	"errors"
	"net/http"
	"time"

	"google.golang.org/api/googleapi"
)

func judge(ctx context.Context, err error, upload bool) (verdict, time.Duration, error) {
	if err != nil && ctx.Err() != nil {
		return unknown, 0, err
	}
	return judged(err, upload)
}

func judged(err error, upload bool) (verdict, time.Duration, error) {
	if err == nil {
		return passed, 0, nil
	}
	var gerr *googleapi.Error
	if !errors.As(err, &gerr) {
		if IsNetwork(err) {
			return offline, 0, err
		}
		return unknown, 0, err
	}
	after := retryAfter(gerr.Header)
	switch {
	case gerr.Code == http.StatusTooManyRequests:
		return rateLimited, after, err
	case gerr.Code >= http.StatusInternalServerError:
		return transient, after, err
	case gerr.Code != http.StatusForbidden || len(gerr.Errors) == 0:
		return refused, 0, err
	}
	return judgedReason(gerr, err, after, upload)
}

func judgedReason(gerr *googleapi.Error, err error, after time.Duration, upload bool) (verdict, time.Duration, error) {
	switch e := gerr.Errors[0]; e.Reason {
	case "storageQuotaExceeded":
		return refused, 0, errors.Join(ErrStorageFull, err)
	case "downloadQuotaExceeded":
		return refused, 0, errors.Join(ErrDownloadQuota, err)
	case "cannotDownloadAbusiveFile":
		return refused, 0, errors.Join(ErrAbusive, err)
	case "userRateLimitExceeded", "rateLimitExceeded":
		if upload && e.Message == uploadLimitMessage {
			return refused, 0, errors.Join(ErrUploadLimit, err)
		}
		return rateLimited, after, err
	}
	return refused, 0, err
}
