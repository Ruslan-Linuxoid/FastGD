package core

import (
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func (c *conflicts) linkShelfID(it meta.Item) (string, error) {
	if it.NeverTried() {
		return "", nil
	}

	id, err := meta.NewLocalKey()
	if err != nil {
		return "", err
	}
	if err := c.killed(stepID); err != nil {
		return "", err
	}
	if err := c.cache.LinkLocal(it.ID, id); err != nil {
		return "", err
	}
	return id, c.killed(stepLink)
}

func (c *conflicts) undoShelfNames(it meta.Item, id string, note *meta.Note) {
	if note != nil {
		if uerr := c.cache.UnlinkLocal(note.ID); uerr != nil {
			c.log.Warn("drop the note of a file that did not go to Lost and Found", "id", it.ID, "err", uerr)
		}
	}
	if id == "" {
		return
	}
	if uerr := c.cache.UnlinkLocal(id); uerr != nil {
		c.log.Warn("drop the second name of a file's content", "id", it.ID, "err", uerr)
	}
}

func (c *conflicts) unlinkShelved(it meta.Item, id string) error {
	if id == "" {
		return nil
	}
	err := c.cache.UnlinkLocal(it.ID)
	if err == nil {
		if err = c.killed(stepUnlink); err != nil {
			return err
		}
		err = c.store.Shelf().Finished(id)
	}
	if err != nil {
		c.log.Error("drop the old name of a file's content", "id", it.ID, "err", err)
	}
	return nil
}

func (c *conflicts) tellShelved(sh meta.Shelved, was string, withNote bool) {
	as, err := c.store.Path(sh.Lost.ID)
	if err != nil {
		as = meta.LostName
	}
	c.log.Warn("Drive holds another version of a file changed here, or none: the local version went to Lost and Found",
		"path", was, "as", as)
	shown := append(sh.Dirs, sh.Lost)
	if withNote {
		shown = append(shown, sh.Note)
	}
	c.shown.stale(shown...)

	c.shown.restated(sh.Lost.Ino)
}
