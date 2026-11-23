package core

import (
	"context"
	"errors"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func (c *conflicts) strandedItem(it meta.Item) bool {
	_, err := c.store.Path(it.ID)
	return errors.Is(err, meta.ErrNotShown)
}

func (c *conflicts) moveStranded(ctx context.Context, it meta.Item, paths map[meta.Ino]string) {
	if it.Service {
		c.dropStrandedService(it)
		return
	}
	was := paths[it.Ino]
	if was == "" {
		var err error
		if was, err = c.store.LastPath(it.ID); err != nil {
			return
		}
	}
	c.resolveNow(ctx, it.Ino, was)
}

func (c *conflicts) dropStrandedService(it meta.Item) {
	if err := c.shown.made(func(v meta.Visible) error { return v.Trash(it.Ino) }); err != nil {
		c.log.Warn("drop a service file of a folder gone from Drive", "id", it.ID, "name", it.Name, "err", err)
		return
	}
	c.forgetUnused(it.Ino)
}

func (c *conflicts) forgetStrandedDirs(dirs []meta.Item) {
	for range dirs {
		forgot, failed := c.forgetRound(dirs)
		if !forgot && !failed {
			break
		}
	}
}

func (c *conflicts) forgetRound(dirs []meta.Item) (forgot, failed bool) {
	for _, dir := range dirs {

		err := c.shown.made(func(v meta.Visible) error {
			gone, err := v.ForgetEmpty(dir.Ino)
			forgot = forgot || gone
			return err
		})
		if err != nil {
			failed = true
			c.log.Warn("forget a folder made here under a folder gone from Drive", "id", dir.ID, "err", err)
		}
	}
	return forgot, failed
}
