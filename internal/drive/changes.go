package drive

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/api/drive/v3"
)

type Change struct {
	FileID string

	Removed bool
	File    *File
}

func (c *Client) Changes(ctx context.Context, token string) ([]Change, string, error) {
	var out []Change
	for {
		page, err := get(ctx, c, costList, func() (*drive.ChangeList, error) {
			return c.svc.Changes.List(token).
				RestrictToMyDrive(true).
				IncludeRemoved(true).
				PageSize(listPageSize).
				Fields("nextPageToken,newStartPageToken,changes(fileId,removed,file(" + fileFields + "))").
				Context(ctx).
				Do()
		})
		if err != nil {
			return nil, "", fmt.Errorf("drive: changes: %w", err)
		}
		for _, ch := range page.Changes {
			chg := Change{FileID: ch.FileId, Removed: ch.Removed || ch.File == nil}
			if !chg.Removed {
				f, err := convert(ch.File)
				if err != nil {
					return nil, "", err
				}
				chg.File = &f
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
