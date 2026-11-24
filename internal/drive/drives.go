package drive

import (
	"context"
	"fmt"

	"google.golang.org/api/drive/v3"
)

type SharedDrive struct {
	ID   string
	Name string
	Can  Rights
}

const driveFields = "id,name,capabilities(canAddChildren,canEdit,canDownload,canTrashChildren,canDeleteChildren)"

func driveOf(d *drive.Drive) SharedDrive {
	out := SharedDrive{ID: d.Id, Name: d.Name, Can: Rights{ModifyContent: true, AddChildren: true,
		RemoveChildren: true, Download: true}}
	if c := d.Capabilities; c != nil {
		out.Can = Rights{ModifyContent: c.CanEdit, AddChildren: c.CanAddChildren, Download: c.CanDownload,
			RemoveChildren: c.CanTrashChildren || c.CanDeleteChildren}
	}
	return out
}

const drivesPageSize = 100

func (c *Client) Drives(ctx context.Context) ([]SharedDrive, error) {
	var out []SharedDrive
	token := ""
	for {
		call := c.svc.Drives.List().PageSize(drivesPageSize).Fields("nextPageToken,drives(" + driveFields + ")").
			Context(ctx)
		if token != "" {
			call = call.PageToken(token)
		}
		page, err := get(ctx, c, costList, func() (*drive.DriveList, error) {
			return call.Do()
		})
		if err != nil {
			return nil, fmt.Errorf("drive: shared drives: %w", err)
		}
		for _, d := range page.Drives {
			if !ValidID(d.Id) {
				return nil, &BadIDError{ID: d.Id}
			}
			out = append(out, driveOf(d))
		}
		if page.NextPageToken == "" {
			return out, nil
		}
		token = page.NextPageToken
	}
}
