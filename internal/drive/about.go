package drive

import (
	"context"
	"fmt"

	"google.golang.org/api/drive/v3"
)

type Quota struct {
	Limit        int64
	Usage        int64
	InDrive      int64
	InDriveTrash int64
}

func (q Quota) Files() int64 { return max(q.InDrive-q.InDriveTrash, 0) }

type Account struct {
	Quota Quota
	Email string
}

func (c *Client) Account(ctx context.Context) (Account, error) {
	a, err := get(ctx, c, costOther, func() (*drive.About, error) {
		return c.svc.About.Get().Fields("storageQuota(limit,usage,usageInDrive,usageInDriveTrash)," +
			"user(emailAddress)").Context(ctx).Do()
	})
	if err != nil {
		return Account{}, fmt.Errorf("drive: about: %w", err)
	}
	var out Account
	if a.StorageQuota != nil {
		q := a.StorageQuota
		out.Quota = Quota{Limit: q.Limit, Usage: q.Usage, InDrive: q.UsageInDrive, InDriveTrash: q.UsageInDriveTrash}
	}
	if a.User != nil {
		out.Email = a.User.EmailAddress
	}
	return out, nil
}
