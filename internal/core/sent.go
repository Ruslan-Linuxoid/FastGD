package core

import (
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/edited"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func sentByCreate(it meta.Item, mtime time.Time) meta.Sent {
	return meta.Sent{Name: it.Name, Parent: it.Parent, Mtime: mtime}
}

func sentByContent(it meta.Item, mtime time.Time) meta.Sent {
	return meta.Sent{Name: it.RName, Parent: it.RParent, Trashed: it.RTrashed, Mtime: mtime}
}

func sentBy(it meta.Item, create bool, mtime time.Time) meta.Sent {
	if create {
		return sentByCreate(it, mtime)
	}
	return sentByContent(it, mtime)
}

func sentBySession(it meta.Item, ses meta.Session, create bool) meta.Sent {
	if create {
		return meta.Sent{Name: ses.Name, Parent: ses.Parent, Mtime: ses.Mtime}
	}
	return sentByContent(it, ses.Mtime)
}

func sentByUpdate(u drive.MetaUpdate) meta.Sent {
	return meta.Sent{Name: u.Name, Parent: u.NewParent, Trashed: u.Trashed, Mtime: u.Modified}
}

func sentOf(rf *drive.File) meta.Sent {
	sent := meta.Sent{Name: rf.Name, Trashed: rf.Trashed, Mtime: rf.Modified}
	if len(rf.Parents) > 0 {
		sent.Parent = rf.Parents[0]
	}
	return sent
}

func remoteOf(rf *drive.File) meta.Remote {
	return meta.Remote{MD5: rf.MD5, Version: rf.Version, Mime: rf.MimeType, Rev: rf.HeadRevision}
}

const (
	emptyIdleUpload = 10 * time.Minute

	againAfter = 2 * time.Second

	sameContentFor = 10 * time.Minute
)

type sent struct {
	at   time.Time
	size int64
	md5  string
}

func (f *FS) noteSent(ino meta.Ino, size int64, md5 string) {
	now := f.clock.Now()
	f.wmu.Lock()
	defer f.wmu.Unlock()
	for other, s := range f.sent {
		if now.Sub(s.at) > sameContentFor {
			delete(f.sent, other)
		}
	}
	f.sent[ino] = sent{at: now, size: size, md5: md5}
}

func (f *FS) lastSent(ino meta.Ino) (sent, bool) {
	f.wmu.Lock()
	defer f.wmu.Unlock()
	s, ok := f.sent[ino]
	return s, ok
}

func (f *FS) uploadDue(ino meta.Ino) time.Time {
	now := f.clock.Now()

	if s, ok := f.lastSent(ino); ok && !s.at.After(now) && s.at.Add(againAfter).After(now) {
		return s.at.Add(againAfter)
	}
	return now
}

func (f *FS) keepSent(it meta.Item) (bool, error) {
	last, ok := f.sentStill(it)
	if !ok {
		return false, nil
	}
	st, gen, size, _ := f.edits.Snap(it)
	if size != last.size {
		return false, nil
	}

	if err := f.edits.Flush(it.ID); err != nil {
		return false, err
	}
	fd, err := f.cache.OpenLocal(it.ID)
	if err != nil {
		return false, err
	}
	defer fd.Close()
	if sum, err := md5Local(fd, size); err != nil || sum != last.md5 {
		return false, err
	}
	err = f.record(st, edited.Upload{Item: it, Gen: gen, Size: size, SentMD5: it.MD5, SentMeta: sentByContent(it,
		it.RMtime),
		Remote: meta.Remote{MD5: it.MD5, Version: it.Version, Mime: it.Mime, Rev: it.Rev}, Same: true})
	return err == nil, err
}

func (f *FS) sentStill(it meta.Item) (sent, bool) {
	last, ok := f.lastSent(it.Ino)
	return last, ok && it.RExists && it.Base <= 0 && it.RMD5 == it.MD5 && last.md5 == it.MD5 &&
		f.clock.Now().Sub(last.at) < sameContentFor
}

func (f *FS) record(st *edited.State, up edited.Upload) error {
	out, err := f.edits.Record(st, up)
	it := up.Item
	if out != edited.Kept && !up.Same {
		f.noteSent(it.Ino, up.Size, up.SentMD5)
	}
	switch {
	case out != edited.Cleaned:
	case up.Same:
		f.log.Info("the content written is the one Drive took a short while ago: it is not uploaded again", "id", it.ID,
			"name", it.Name, "within", sameContentFor)
	default:
		f.log.Info("uploaded", "id", it.ID, "name", it.Name, "size", up.Size)
		f.quotaStale.Store(true)
	}
	return err
}
