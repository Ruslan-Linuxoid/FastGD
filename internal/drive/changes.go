package drive

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/api/drive/v3"
)

type Change struct {
	FileID string

	Removed bool
	File    *File

	Drive *SharedDrive
}

func (c *Client) Changes(ctx context.Context, token string) ([]Change, string, error) {
	var out []Change
	for {
		page, err := get(ctx, c, costList, func() (*drive.ChangeList, error) {
			list, err := c.svc.Changes.List(token).
				IncludeItemsFromAllDrives(true).
				SupportsAllDrives(true).
				IncludeRemoved(true).
				PageSize(int64(c.changesPage.get())).
				Fields("nextPageToken,newStartPageToken,changes(changeType,fileId,removed,driveId,drive(" + driveFields +
					"),file(" + fileFields + "))").
				Context(ctx).
				Do()
			c.changesPage.answered(err, time.Now())
			return list, err
		})
		if err != nil {
			return nil, "", fmt.Errorf("drive: changes: %w", err)
		}
		for _, ch := range page.Changes {
			chg, err := changeOf(ch)
			if err != nil {
				return nil, "", err
			}
			out = append(out, chg)
		}
		if page.NewStartPageToken != "" {
			return out, page.NewStartPageToken, nil
		}
		if page.NextPageToken == "" {
			return nil, "", errors.New("drive: changes page without a next token")
		}
		token = page.NextPageToken
	}
}

func changeOf(ch *drive.Change) (Change, error) {
	if ch.ChangeType == "drive" {
		return driveChange(ch)
	}
	chg := Change{FileID: ch.FileId, Removed: ch.Removed || ch.File == nil}
	if !chg.Removed {
		f, err := convert(ch.File)
		if err != nil {
			return Change{}, err
		}
		chg.File = &f
	}
	return chg, nil
}

func driveChange(ch *drive.Change) (Change, error) {
	if !ValidID(ch.DriveId) {
		return Change{}, &BadIDError{ID: ch.DriveId}
	}
	if ch.Removed || ch.Drive == nil {
		return Change{Removed: true, Drive: &SharedDrive{ID: ch.DriveId}}, nil
	}
	d := driveOf(ch.Drive)
	d.ID = ch.DriveId
	return Change{Drive: &d}, nil
}
