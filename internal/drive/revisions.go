package drive

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"google.golang.org/api/drive/v3"
	"google.golang.org/api/googleapi"
)

var ErrNoRevision = errors.New("drive: no such revision")

var ErrRevisionRefused = errors.New("drive: Drive refuses the account the revision")

func revisionErr(id, rev string, err error) error {
	if hasCode(err, http.StatusNotFound) {
		err = ErrNoRevision
	}
	return fmt.Errorf("drive: revision %s of %s: %w", rev, id, err)
}

func (c *Client) RevisionKept(ctx context.Context, id, rev string) (bool, error) {
	r, err := get(ctx, c, costOther, func() (*drive.Revision, error) {
		return c.svc.Revisions.Get(id, rev).Fields("keepForever").Context(ctx).Do()
	})
	if err != nil {
		return false, revisionErr(id, rev, err)
	}
	return r.KeepForever, nil
}

func (c *Client) KeepRevision(ctx context.Context, id, rev string, keep bool) error {
	_, err := get(ctx, c, costOtherWrite, func() (*drive.Revision, error) {
		return c.svc.Revisions.Update(id, rev, &drive.Revision{KeepForever: keep, ForceSendFields: []string{"KeepForever"}}).
			Fields("keepForever").Context(ctx).Do()
	})
	if err != nil {
		return revisionErr(id, rev, err)
	}
	return nil
}

func (c *Client) StreamRevision(ctx context.Context, id, rev string, off, length int64, sink func([]byte) error) error {
	err := c.stream(ctx, id+" revision "+rev, off, length, sink, func(ctx context.Context, rng string) (*http.Response,
		error) {
		call := c.svc.Revisions.Get(id, rev).AcknowledgeAbuse(true).Context(ctx)
		call.Header().Set("Range", rng)
		return call.Download()
	})
	switch {
	case hasCode(err, http.StatusNotFound):
		return fmt.Errorf("%w: %w", ErrNoRevision, err)
	case refusedForRights(err):
		return fmt.Errorf("%w: %w", ErrRevisionRefused, err)
	}
	return err
}

func refusedForRights(err error) bool {
	gerr, ok := errors.AsType[*googleapi.Error](err)
	return ok && gerr.Code == http.StatusForbidden && (hasReason(gerr, "downloadRestrictedForRevision") ||
		hasReason(gerr, "download_restricted_for_revision") || hasReason(gerr, "insufficientFilePermissions"))
}
