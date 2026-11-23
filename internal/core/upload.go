package core

import (
	"context"

	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync/atomic"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/edited"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func (f *FS) uploadFile(ctx context.Context, it meta.Item) error {
	if err := f.edits.FillBase(ctx, it); err != nil {
		return err
	}
	st, gen, size, mtime := f.edits.Snap(it)

	if err := f.edits.Flush(it.ID); err != nil {
		return err
	}
	fd, err := f.cache.OpenLocal(it.ID)
	if err != nil {
		return err
	}
	defer fd.Close()
	snap := snapshot{gen: gen, size: size, mtime: mtime, fd: fd}
	rf, sent, sentMeta, err := f.sendContent(ctx, it, st, snap, !it.RExists)
	if errors.Is(err, drive.ErrExists) {
		prior, priorMeta, done, perr := f.takePriorCreate(ctx, it, st, snap)
		if done {
			return perr
		}
		it.Mime = prior.MimeType
		it.RName, it.RParent, it.RTrashed = priorMeta.Name, priorMeta.Parent, priorMeta.Trashed
		rf, sent, sentMeta, err = f.sendContent(ctx, it, st, snap, false)
	}
	if errors.Is(err, errOutdated) {
		return nil
	}
	if err != nil {
		return err
	}
	if rf, err = f.withMD5(ctx, rf); err != nil {
		return err
	}
	f.countUpload(size)
	f.acceptedBytes(size)
	return f.record(st, edited.Upload{Item: it, Gen: gen, Size: size, Remote: remoteOf(rf), SentMD5: sent,
		SentMeta: sentMeta})
}

func (f *FS) takePriorCreate(
	ctx context.Context, it meta.Item, st *edited.State, snap snapshot,
) (prior *drive.File, priorMeta meta.Sent, done bool, err error) {
	prior, priorMeta, err = f.existing(ctx, it.ID)
	if err != nil {
		return nil, meta.Sent{}, true, err
	}
	if f.toItem(*prior).Kind != meta.KindFile {

		f.log.Warn("the fileId of a file made here holds an item of another kind in Drive", "id", it.ID, "name", it.Name,
			"type", prior.MimeType)
		err = f.conflicts.shelve(ctx, it, prior, "", "под fileId этого файла в Drive оказался элемент другого вида")
		return prior, priorMeta, true, err
	}
	if prior.Size != snap.size {
		return prior, priorMeta, false, nil
	}

	if sum, serr := md5Local(snap.fd, snap.size); serr == nil && sum == prior.MD5 {
		f.countUpload(snap.size)
		f.acceptedBytes(snap.size)
		err = f.record(st, edited.Upload{Item: it, Gen: snap.gen, Size: snap.size, Remote: remoteOf(prior), SentMD5: sum,
			SentMeta: priorMeta})
		return prior, priorMeta, true, err
	}
	return prior, priorMeta, false, nil
}

func (f *FS) withMD5(ctx context.Context, rf *drive.File) (*drive.File, error) {
	if rf.MD5 != "" {
		return rf, nil
	}
	got, err := f.opt.Drive.Get(ctx, rf.ID)
	if err != nil {
		return rf, fmt.Errorf("the md5 of the upload: %w", err)
	}
	return got, nil
}

func (f *FS) sendContent(
	ctx context.Context, it meta.Item, st *edited.State, snap snapshot, create bool,
) (*drive.File, string, meta.Sent, error) {
	if snap.size >= f.sessionThreshold() {
		return f.sendResumable(ctx, it, st, snap, create)
	}

	var b *sealedBody
	var outdated atomic.Bool
	body := func() io.Reader {

		b = &sealedBody{st: st, snap: snap, sum: md5.New(), outdated: &outdated}
		return b
	}
	var rf *drive.File
	var err error
	if create {
		rf, err = f.opt.Drive.CreateFile(ctx, it.ID, it.Name, it.Parent, newMime(it), snap.mtime, snap.size, body)
	} else {
		rf, err = f.opt.Drive.UpdateContent(ctx, it.ID, it.Mime, snap.mtime, snap.size, body)
	}
	if outdated.Load() {

		return nil, "", meta.Sent{}, errOutdated
	}
	if err != nil {
		return nil, "", meta.Sent{}, err
	}
	return rf, hex.EncodeToString(b.sum.Sum(nil)), sentBy(it, create, snap.mtime), nil
}
