package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/cache"
	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/edited"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func Open(ctx context.Context, opt Options) (*FS, error) {
	opt.defaults()
	if opt.FileMode == 0 {
		opt.FileMode = 0o600
	}
	if opt.DirMode == 0 {
		opt.DirMode = 0o700
	}
	if err := secureCacheDir(opt.CacheDir); err != nil {
		return nil, err
	}
	store, err := openStore(opt)
	if err != nil {
		return nil, err
	}
	f, err := newFS(opt, store)
	if err != nil {
		store.Close()
		return nil, err
	}
	f.conflicts = &conflicts{
		store: store, cache: f.cache, edits: f.edits, queue: f.queue, shown: f.shown,
		log: opt.Log, clock: opt.Clock, drive: opt.Drive, fault: opt.ShelveFault,
		toItem: f.toItem, record: f.record, recorded: f.recorded, finished: f.finished,
		forgetUnused: f.forgetUnused, unuploadedPaths: f.unuploadedPaths,
	}
	f.upMeter.SetLimit(opt.Drive.Speed().Upload)
	for _, step := range f.startup(ctx) {
		f.log.Debug("startup", "step", step.name)
		if err := step.run(); err != nil {
			f.Close()
			return nil, err
		}
	}
	opt.Drive.OnReconnect(f.reconnected)
	return f, nil
}

func secureCacheDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("core: %w", err)
	}

	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("core: %w", err)
	}
	return nil
}

func openStore(opt Options) (*meta.Store, error) {

	setAsideAt := opt.Clock.Now()
	aside, why, err := setAside(opt.CacheDir, setAsideAt)
	if err != nil {
		return nil, fmt.Errorf("core: %w", err)
	}
	store, err := meta.Open(DBPath(opt.CacheDir))
	if errors.Is(err, meta.ErrNewerSchema) {
		if merr := markUntrusted(opt.CacheDir, err.Error()); merr != nil {
			return nil, errors.Join(err, merr)
		}
		return nil, fmt.Errorf("%w: %w", ErrUntrusted, err)
	}
	if err != nil {
		return nil, err
	}
	if err := noteAside(opt, store, aside, why, setAsideAt); err != nil {
		store.Close()
		return nil, err
	}
	return store, nil
}

func noteAside(opt Options, store *meta.Store, aside, why string, setAsideAt time.Time) error {
	var err error
	if aside == "" {

		if aside, err = unrecordedCopy(opt.CacheDir, store); err != nil {
			return err
		}
		why = "the reason went with the start that set it aside"
	}
	if aside == "" {
		return nil
	}
	copies := StateCopies(opt.CacheDir)
	opt.Log.Warn("the state was not to be trusted: it is set aside, and the daemon starts from nothing; the copies "+
		"stay until the owner removes them", "copy", aside, "why", why, "copies", len(copies))
	if err := store.SetState(meta.StateRestoreFrom, aside); err != nil {
		return err
	}
	return store.SetDiscarded(setAsideAt, aside, why)
}

func newFS(opt Options, store *meta.Store) (*FS, error) {
	f := &FS{
		distrusted: make(chan struct{}),
		opt:        opt, store: store, log: opt.Log, clock: opt.Clock,
		upload:    make(chan struct{}, maxUploads),
		pollMu:    make(pollLock, 1),
		queue:     newQueue(store, opt.Log),
		shown:     newShown(store, opt.Log),
		refusals:  newRefusals(),
		sent:      map[meta.Ino]sent{},
		idsWanted: make(chan struct{}, 1),
	}
	var err error
	f.cache, err = cache.New(cache.Options{
		Dir:          ContentDir(opt.CacheDir),
		Records:      store,
		Source:       f.download,
		Passing:      drive.Passing,
		Log:          opt.Log,
		Size:         opt.CacheSize,
		Share:        opt.PrefetchShare,
		TransferTime: opt.TransferTime,
		Free:         opt.CacheFree,
		MinLimit:     opt.MinCacheLimit,
		MinFree:      opt.MinCacheFree,
		Clock:        opt.Clock,
		MaxSpeed:     opt.Drive.Speed().Download,
		HoleUnit:     opt.HoleUnit,
		Mixed:        f.mixed,
		LimitChanged: f.cacheLimitChanged,
	})
	if err != nil {
		return nil, err
	}
	f.edits = edited.Open(edited.Options{
		Disk: f.cache, Store: store, Marks: store, Drive: opt.Drive, Source: f.download, Clock: opt.Clock, Log: opt.Log,
		Room: f.room, UploadDue: f.uploadDue, Backoff: taskBackoff, RecoverFault: opt.RecoverFault,
		Resized: func(ino meta.Ino) { f.shown.restated(ino) }, ReadAnew: f.readAnew,
		GiveUpBase: func(ctx context.Context, ino meta.Ino, cause error) error {
			return f.conflicts.baseGivenUp(ctx, ino, cause)
		},
	}, f.cache.SyncLocal)
	return f, nil
}
