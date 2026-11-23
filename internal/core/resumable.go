package core

import (
	"bytes"
	"context"

	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/edited"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"github.com/Ruslan-Linuxoid/FastGD/internal/speed"
)

const resumableFrom = 100 << 20

func (f *FS) sessionThreshold() int64 {
	if f.opt.ResumableFrom > 0 {
		return f.opt.ResumableFrom
	}
	return resumableFrom
}

const maxStalls = 3

var errOutdated = errors.New("written during the upload")

type snapshot struct {
	gen   int64
	size  int64
	mtime time.Time
	fd    *os.File
}

func (f *FS) sendResumable(
	ctx context.Context, it meta.Item, st *edited.State, snap snapshot, create bool,
) (*drive.File, string, meta.Sent, error) {
	traced := f.upMeter.Trace(ctx)
	ses, p, err := f.openSession(traced, it, st, snap, create)
	if err != nil {
		return nil, "", meta.Sent{}, err
	}
	u := &resumeUpload{it: it, st: st, snap: snap, create: create, ses: ses, p: p}
	for u.p.Done == nil {
		if err := f.resumeStep(ctx, traced, u); err != nil {
			return nil, "", meta.Sent{}, err
		}
	}
	sum := u.tail.md5
	if u.tail.bytes == nil {

		if sum, err = md5Local(snap.fd, snap.size); err != nil {
			return nil, "", meta.Sent{}, err
		}
	}
	return u.p.Done, sum, sentBySession(it, u.ses, create), nil
}

func (f *FS) openSession(
	traced context.Context, it meta.Item, st *edited.State, snap snapshot, create bool,
) (meta.Session, drive.Progress, error) {
	ses, err := f.store.UploadSession(it.Ino)
	if err != nil {
		return meta.Session{}, drive.Progress{}, err
	}
	if ses.Address != "" {
		p, err := f.opt.Drive.UploadStatus(traced, ses.Address, snap.size)
		if !errors.Is(err, drive.ErrSessionGone) {
			return ses, p, err
		}
		f.log.Info("Drive forgot the upload session, the upload starts again", "id", it.ID)
	}
	ses, err = f.newSession(traced, it, st, snap, create)
	return ses, drive.Progress{}, err
}

func (f *FS) newSession(
	traced context.Context, it meta.Item, st *edited.State, snap snapshot, create bool,
) (meta.Session, error) {
	sent := sentBy(it, create, snap.mtime)
	ses := meta.Session{Name: sent.Name, Parent: sent.Parent, Mtime: sent.Mtime}
	var err error
	if create {
		ses.Address, err = f.opt.Drive.StartCreate(traced, it.ID, it.Name, it.Parent, newMime(it), snap.mtime, snap.size)
	} else {
		ses.Address, err = f.opt.Drive.StartUpdate(traced, it.ID, it.Mime, snap.mtime, snap.size)
	}
	if err != nil {
		return meta.Session{}, err
	}
	return ses, f.keepSession(it.Ino, st, snap.gen, ses)
}

const chunkUnit = 256 << 10

func tailFrom(size int64) int64 { return (size - 1) / chunkUnit * chunkUnit }

func (f *FS) sendChunk(
	ctx context.Context, addr string, off int64, snap snapshot, tail sealed,
) (drive.Progress, error) {
	if off >= snap.size {
		return f.opt.Drive.UploadStatus(f.upMeter.Trace(ctx), addr, snap.size)
	}
	if f.beforeChunk != nil {
		f.beforeChunk(off)
	}
	from := tailFrom(snap.size)
	var n int64
	var content io.Reader
	if off < from {

		end := (off + f.upMeter.Share(f.opt.TransferTime) + chunkUnit - 1) / chunkUnit * chunkUnit
		n = min(end, from) - off
		content = io.NewSectionReader(snap.fd, off, n)
	} else {
		n = snap.size - off
		content = bytes.NewReader(tail.bytes[off-from:])
	}
	body := &meteredReader{r: content, m: &f.upMeter}
	p, err := f.opt.Drive.SendChunk(ctx, addr, off, n, snap.size, body)
	body.done(err == nil, n)
	return p, err
}

func (f *FS) keepSession(ino meta.Ino, st *edited.State, gen int64, ses meta.Session) error {
	kept, err := st.KeepSession(gen, func() error { return f.store.SetUploadSession(ino, ses) })
	if err == nil && !kept {
		err = errOutdated
	}
	return err
}

func md5Local(fd *os.File, size int64) (string, error) {

	h := md5.New()
	if _, err := io.Copy(h, io.NewSectionReader(fd, 0, size)); err != nil {
		return "", fmt.Errorf("md5 of the local content: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type meteredReader struct {
	r       io.Reader
	m       *speed.Meter
	mu      sync.Mutex
	started bool
	ended   bool
}

func (r *meteredReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	r.mu.Lock()
	defer r.mu.Unlock()
	if n > 0 && !r.started && !r.ended {
		r.started = true
		r.m.Arrive(0, true, time.Now())
	}
	return n, err
}

func (r *meteredReader) done(ok bool, n int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ended = true
	if !r.started {
		return
	}
	if !ok {
		n = 0
	}
	r.m.Answered(int(n), time.Now())
}
