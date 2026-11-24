package drive

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/api/drive/v3"
)

type MetaUpdate struct {
	Name      string
	OldParent string
	NewParent string
	Trashed   bool
	Modified  time.Time
}

func (c *Client) driveTime(ctx context.Context, id string) (string, error) {
	rf, err := get(ctx, c, costOther, func() (*drive.File, error) {
		return c.svc.Files.Get(id).Fields("modifiedTime").SupportsAllDrives(true).Context(ctx).Do()
	})
	if err != nil {
		return "", fmt.Errorf("drive: the time of %s: %w", id, err)
	}
	return rf.ModifiedTime, nil
}

func (c *Client) UpdateMeta(ctx context.Context, id string, u MetaUpdate) (*File, error) {
	f := &drive.File{Trashed: u.Trashed, ForceSendFields: []string{"Trashed"}}
	if u.Name != "" {
		f.Name = u.Name
	}
	switch {
	case atBound(u.Modified):

		raw, err := c.driveTime(ctx, id)
		if err != nil {
			return nil, err
		}
		f.ModifiedTime = raw
	case !u.Modified.IsZero():
		f.ModifiedTime = stamp(u.Modified)
	}
	call := c.svc.Files.Update(id, f).Fields(fileFields).SupportsAllDrives(true).Context(ctx)
	if u.NewParent != "" && u.NewParent != u.OldParent {
		call = call.AddParents(u.NewParent)
		if u.OldParent != "" {
			call = call.RemoveParents(u.OldParent)
		}
	}
	rf, err := get(ctx, c, costWrite, func() (*drive.File, error) {
		return call.Do()
	})
	return stored(rf, err, "update "+id)
}
