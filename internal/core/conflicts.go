package core

import (
	"context"

	"crypto/md5"
	"encoding/hex"
	"io"
	"log/slog"

	"github.com/Ruslan-Linuxoid/FastGD/internal/cache"
	"github.com/Ruslan-Linuxoid/FastGD/internal/clock"
	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/edited"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

type conflicts struct {
	store *meta.Store
	cache *cache.Cache
	edits *edited.Files
	queue *queue
	shown *shown
	log   *slog.Logger
	clock clock.Clock
	drive interface {
		Get(ctx context.Context, id string) (*drive.File, error)
	}

	fault func(step string) error

	toItem          func(drive.File) meta.Item
	record          func(*edited.State, edited.Upload) error
	recorded        func(meta.Item) (meta.Item, error)
	finished        func(meta.Ino)
	forgetUnused    func(meta.Ino)
	unuploadedPaths func() (map[meta.Ino]string, error)
}

func (c *conflicts) resolve(ctx context.Context, it meta.Item) (bool, meta.Item, error) {
	if !it.InDrive() {
		return false, it, nil
	}
	rf, err := c.remote(ctx, it.ID)
	if err != nil {
		return false, it, err
	}
	if done, err := c.giveWay(ctx, it, rf); done {
		return true, it, err
	}
	return c.takeRenamedInDrive(it, rf)
}

func (c *conflicts) giveWay(ctx context.Context, it meta.Item, rf *drive.File) (bool, error) {
	if rf == nil || rf.Trashed {
		return true, c.giveWayToGone(ctx, it, rf)
	}
	if it.Kind != meta.KindFile || rf.MD5 == it.MD5 {
		return false, nil
	}
	switch {

	case it.TrashedHere():
		c.log.Warn("a file deleted here was changed in Drive: it is back with Drive's version", "id", it.ID, "name", rf.Name)
		return true, c.takeRemote(it, rf)
	case it.Dirty:
		if own, err := c.ownUpload(ctx, it, rf); own || err != nil {
			return true, err
		}
		return true, c.shelve(ctx, it, rf, "", whyChangedInDrive)
	}
	return false, nil
}

func (c *conflicts) giveWayToGone(ctx context.Context, it meta.Item, rf *drive.File) error {
	deleted := it.TrashedHere()
	if it.Kind == meta.KindFile && it.Dirty && !deleted {
		return c.shelveOrDrop(ctx, it, rf, "", whyGoneInDrive)
	}
	if !deleted {
		c.log.Warn("an item changed here is deleted in Drive: it stays deleted", "id", it.ID, "name", it.Name)
	}
	return c.takeRemote(it, rf)
}

func (c *conflicts) takeRenamedInDrive(it meta.Item, rf *drive.File) (bool, meta.Item, error) {
	theirs := c.toItem(*rf)
	if !renamedBoth(it, theirs) {
		return false, it, nil
	}
	if err := c.shown.local(func(v meta.Visible) error { return c.takeRemoteName(v, it, theirs) }, it,
		theirs); err != nil {
		return false, it, err
	}
	now, err := c.store.ByIno(it.Ino)
	if err != nil {
		return false, it, err
	}
	return !now.AheadOfDrive(), now, nil
}

func (c *conflicts) ownUpload(ctx context.Context, it meta.Item, rf *drive.File) (bool, error) {
	if it.Base > 0 {

		lacks, err := c.edits.LacksBase(ctx, edited.Local{Ino: it.Ino, ID: it.ID})
		if err != nil || lacks {
			return false, err
		}
	}
	st, gen, size, _ := c.edits.Snap(it)
	if size != rf.Size {
		return false, nil
	}
	fd, err := c.cache.OpenLocal(it.ID)
	if err != nil {
		return false, err
	}
	defer fd.Close()

	sum := md5.New()
	if _, err := io.CopyN(sum, fd, size); err != nil {
		return false, err
	}
	sent := hex.EncodeToString(sum.Sum(nil))
	if sent != rf.MD5 {
		return false, nil
	}
	c.log.Info("Drive holds an upload the database did not record: it is recorded", "id", it.ID, "name", it.Name)
	return true, c.record(st, edited.Upload{Item: it, Gen: gen, Size: size, Remote: remoteOf(rf), SentMD5: sent,
		SentMeta: sentOf(rf)})
}

func renamedBoth(mine, theirs meta.Item) bool {
	there := theirs.Name != mine.RName || theirs.Parent != mine.RParent
	return mine.RenamedHere() && there && (theirs.Name != mine.Name || theirs.Parent != mine.Parent)
}

func (c *conflicts) resolveChanged(
	ctx context.Context, applied []meta.Applied, renamed []meta.Item, paths map[meta.Ino]string,
) error {
	for _, theirs := range renamed {
		mine, err := c.store.ByID(theirs.ID)
		if err == nil {
			err = c.shown.local(func(v meta.Visible) error { return c.takeRemoteName(v, mine, theirs) }, mine, theirs)
		}
		if err != nil {
			return err
		}
	}
	for _, a := range applied {
		if a.After != nil && a.After.Waits() {
			c.resolveNow(ctx, a.After.Ino, "")
		}
	}
	c.stranded(ctx, paths)
	return nil
}

func (c *conflicts) resolveNow(ctx context.Context, ino meta.Ino, was string) {
	if !c.queue.hold(ino) {
		return
	}
	defer c.finished(ino)
	it, err := c.store.ByIno(ino)
	if err == nil {
		it, err = c.recorded(it)
	}
	if err == nil {
		var waits bool
		if waits, err = c.waitsForBase(ctx, it); waits {
			return
		}
	}
	if err == nil {
		err = c.settle(ctx, it, was)
	}
	if err != nil && ctx.Err() == nil {
		c.log.Warn("settle an item changed here and in Drive", "ino", ino, "err", err)
	}
}

func (c *conflicts) waitsForBase(ctx context.Context, it meta.Item) (bool, error) {
	if it.Base <= 0 {
		return false, nil
	}
	lacks, err := c.edits.LacksBase(ctx, edited.Local{Ino: it.Ino, ID: it.ID})
	return err == nil && lacks, err
}

func (c *conflicts) settle(ctx context.Context, it meta.Item, was string) error {
	switch {
	case was == "":
		_, _, err := c.resolve(ctx, it)
		return err
	case it.RExists:
		rf, err := c.remote(ctx, it.ID)
		if err != nil {
			return err
		}
		return c.shelveOrDrop(ctx, it, rf, was, whyFolderGone)
	default:
		return c.shelve(ctx, it, nil, was, whyFolderGone)
	}
}
