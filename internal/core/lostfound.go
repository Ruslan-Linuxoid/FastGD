package core

import (
	"context"
	"errors"
	"path"
	"strings"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/edited"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

const (
	whyBaseGivenUp = "файл изменён здесь, а Drive не отдал части его версии, которых не хватало для выгрузки; " +
		"части, которые не удалось скачать из Drive, в этой копии пустые. Последний ответ Drive: "
	whyChangedInDrive = "файл изменён здесь, а в Drive за это время появилась другая его версия"
	whyGoneInDrive    = "файл изменён здесь, а в Drive за это время удалён"
	whyFolderGone     = "файл изменён здесь, а папка, в которой он лежал, ушла из Drive"
)

func noteName(name string) string { return name + " (записка).txt" }

func (c *conflicts) writeNote(name, was, why string, now time.Time) (*meta.Note, error) {
	text := "Файл: " + name + "\n" +
		"Лежал в Drive: " + was + "\n" +
		"Попал сюда: " + now.Local().Format("2006-01-02 15:04:05 -0700") + "\n" +
		"Причина: " + why + "\n"

	id, err := meta.NewLocalKey()
	if err != nil {
		return nil, err
	}
	if err := c.cache.PutLocal(id, []byte(text)); err != nil {
		return nil, err
	}
	return &meta.Note{ID: id, Name: noteName(name), Size: int64(len(text))}, nil
}

func conflicted(name string, at time.Time) string {
	ext := path.Ext(name)
	if ext == name {

		ext = ""
	}
	return strings.TrimSuffix(name, ext) + " (конфликт " + at.Local().Format("2006-01-02 15-04-05") + ")" + ext
}

func (c *conflicts) shelve(ctx context.Context, it meta.Item, remote *drive.File, was, why string) error {
	if it.Base > 0 {
		if err := c.edits.FetchBase(ctx, it.Ino); err != nil {
			return err
		}
	}
	_, err := c.shelveAsIs(it, remote, was, why)
	return err
}

func (c *conflicts) shelveAsIs(it meta.Item, remote *drive.File, was, why string) (bool, error) {
	st := c.edits.Lock(it)
	defer st.Unlock()
	it, err := c.store.ByIno(it.Ino)
	if err != nil || !it.ContentLocal() || it.LocalOnly() {
		return false, err
	}
	if was == "" {
		if was, err = c.store.LastPath(it.ID); err != nil {
			return false, err
		}
	}
	dirs := pathDirs(was)
	err = c.shown.local(func(v meta.Visible) error { return c.shelveLocked(v, it, st, remote, dirs, was, why) }, it)
	return err == nil, err
}

func (c *conflicts) shelveLocked(
	v meta.Visible, it meta.Item, st *edited.State, remote *drive.File, dirs []string, was, why string,
) error {
	id, err := c.linkShelfID(it)
	if err != nil {
		return err
	}
	now := c.clock.Now()
	name := conflicted(it.Name, now)
	note, err := c.writeNote(name, was, why, now)
	if err != nil {
		c.log.Warn("write the note beside a file in Lost and Found", "path", was, "err", err)
	}
	var sh meta.Shelved
	err = c.edits.DropBaseIn(it.Ino, func() error {
		var err error
		sh, err = v.Shelve(it, id, dirs, name, c.asDriveHolds(remote), note, now)
		return err
	})
	if err != nil {
		c.undoShelfNames(it, id, note)
		return err
	}
	if err := c.killed(stepRows); err != nil {
		return err
	}
	if err := c.unlinkShelved(it, id); err != nil {
		return err
	}
	c.edits.Moved(st, sh.Lost)
	c.tellShelved(sh, was, note != nil)
	return nil
}

func (c *conflicts) shelveOrDrop(ctx context.Context, it meta.Item, remote *drive.File, was, why string) error {
	err := c.shelve(ctx, it, remote, was, why)
	if remote != nil || !errors.Is(err, edited.ErrBaseLost) {
		return err
	}
	if was == "" {

		if was, _ = c.store.LastPath(it.ID); was == "" {
			was = it.Name
		}
	}
	c.log.Error("a file changed here was deleted in Drive for good, and the working copy lacks a part of the version "+
		"the change was made on: the change is dropped",
		"path", was, "why", err)
	return c.takeRemote(it, nil)
}

func (c *conflicts) resume() error {
	shelf := c.store.Shelf()
	moved, err := shelf.Unfinished()
	if err != nil {
		return err
	}
	for as, had := range moved {
		if err := c.cache.UnlinkShared(had, as); err != nil {
			return err
		}
		if err := shelf.Finished(as); err != nil {
			return err
		}
	}
	return nil
}

func pathDirs(was string) []string {
	dir := path.Dir(was)
	if dir == "." {
		return nil
	}
	return strings.Split(dir, "/")
}
