package core

import (
	"context"
	"errors"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/edited"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func (c *conflicts) takeRemoteName(v meta.Visible, it, theirs meta.Item) error {
	if err := v.TakeRemoteName(it, theirs.Name, theirs.Parent); err != nil {
		return err
	}
	c.log.Warn("an item renamed or moved here was renamed or moved in Drive too: Drive's name and folder stay",
		"id", it.ID, "here", it.Name, "drive", theirs.Name)
	return nil
}

func (c *conflicts) takeRemote(it meta.Item, remote *drive.File) error {
	taken := false
	err := c.shown.local(func(v meta.Visible) error {
		var st *edited.State
		if it.ContentLocal() {
			st = c.edits.Lock(it)
			defer st.Unlock()
		}
		if err := v.TakeRemote(it.Ino, c.asDriveHolds(remote)); err != nil {
			return err
		}
		taken = true
		if st == nil {
			return nil
		}
		if err := c.killed(stepTakeRemote); err != nil {
			return err
		}

		err := c.cache.Remove(it.Ino, it.ID)
		c.edits.GaveWay(it.Ino, st)
		return err
	}, it)

	if taken {
		c.shown.rewritten(it.Ino)
	}
	return err
}

func (c *conflicts) remote(ctx context.Context, id string) (*drive.File, error) {
	rf, err := c.drive.Get(ctx, id)
	if errors.Is(err, drive.ErrNotFound) {
		return nil, nil
	}
	return rf, err
}

func (c *conflicts) asDriveHolds(remote *drive.File) *meta.Item {
	if remote == nil {
		return nil
	}
	it := c.toItem(*remote)
	it.Trashed = remote.OwnTrashed
	return &it
}
