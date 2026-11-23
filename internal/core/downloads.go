package core

import (
	"errors"
	"sync/atomic"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func (f *FS) noteDownload(id string, err error) {

	f.dlMu.Lock()
	defer f.dlMu.Unlock()
	switch {
	case errors.Is(err, drive.ErrAbusive):
		f.abRefused.Store(&id)
		f.log.Warn("Drive refused the content of a file it flagged as abusive, which it gives to the owner alone; opens of "+
			"it get EACCES", "id", id)
		if err := f.store.RefuseAbusive(f.clock.Now(), id); err != nil {
			f.log.Warn("keep the refusal of an abusive file", "err", err)
		}
	case errors.Is(err, drive.ErrDownloadQuota):
		if switched(&f.dlRefused, id) {
			f.log.Warn("Drive refused a download by its download quota; reads of what is not cached get EIO", "id", id)
		}
		if err := f.store.SetDownloadRefused(f.clock.Now(), id); err != nil {
			f.log.Warn("keep the refusal of a download", "err", err)
		}
	case err == nil:
		f.cleared(&f.dlRefused, id, meta.StateDownloadRefused, "Drive lets the refused file download again")
		f.cleared(&f.abRefused, id, meta.StateAbuseRefused, "Drive lets the flagged file download again")
	}
}

func (f *FS) cleared(kept *atomic.Pointer[string], id, state, msg string) {
	last := kept.Load()
	if last == nil || *last != id || !kept.CompareAndSwap(last, nil) {
		return
	}
	f.log.Info(msg, "id", id)
	if err := f.store.DeleteState(state); err != nil {
		f.log.Warn("drop the refusal of a download", "err", err)
	}
}

func (f *FS) loadDownloadRefusal() {
	if id, ok := f.store.Refused(meta.StateDownloadRefused); ok {
		f.dlRefused.Store(&id)
	}
	if id, ok := f.store.Refused(meta.StateAbuseRefused); ok {
		f.abRefused.Store(&id)
	}
}
