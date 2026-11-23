package core

import (
	"context"
	"sync"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/edited"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func (f *FS) Store() *meta.Store { return f.store }

func (f *FS) SignedOut(err error) { f.signedOut(err) }

func (f *FS) Edits() *edited.Files { return f.edits }

func (f *FS) CheckCache(ctx context.Context, it meta.Item) error {
	return f.cache.Check(ctx, f.edits.FileOf(it))
}

func (f *FS) RefreshQuota(ctx context.Context) error { return f.refreshQuota(ctx) }

func (f *FS) WindowCap() int64 { return f.cache.WindowCap() }

func LimitBackoff(n int) time.Duration { return limitBackoff(n) }

func (f *FS) SaveUsage() { f.saveUsage() }

func (f *FS) BeforeRecord(fn func() error) { f.edits.BeforeRecord(fn) }

func (f *FS) SaveSizes() { f.edits.SaveSizes() }

func (f *FS) WarnDirtyOver() { f.warnDirtyOver() }

func (f *FS) ReadGone(ctx context.Context) {
	f.readGone(ctx)
	f.bg.Wait()
}

func (f *FS) CacheLimit() int64 { return f.cache.Limit() }

func (f *FS) CacheUsed() int64 { return f.cache.Used() }

func (f *FS) SeedUploadSpeed(bytesPerSecond int) {
	at := time.Unix(0, 0)
	f.upMeter.Arrive(0, true, at)
	f.upMeter.Arrive(bytesPerSecond, false, at.Add(time.Second))
	f.upMeter.Leave(at.Add(time.Second))
}

func (f *FS) BeforeChunk(fn func(off int64)) { f.beforeChunk = fn }

func (f *FS) SyncLocal(fn func(id string) error) { f.edits.SyncLocal(fn) }

const Sealing = sealing

func (f *FS) GiveID(ino uint64) (string, error) {
	it, err := f.store.ByIno(meta.Ino(ino))
	if err != nil {
		return "", err
	}
	it, err = f.giveID(context.Background(), it)
	return it.ID, err
}

func (f *FS) UnkeepIdle(ctx context.Context) { f.edits.UnkeepIdle(ctx) }

func UsageLevelAfter(n, free int64, was int) int { return usageLevelAfter(n, free, was) }

func (f *FS) AcceptedBytes(size int64) { f.acceptedBytes(size) }

func (f *FS) MarkChangedHere(ino uint64) error {
	it, err := f.store.ByIno(meta.Ino(ino))
	if err != nil {
		return err
	}
	return f.edits.MakeDirty(it)
}

func (f *FS) WindowCapOf(ino uint64) int64 { return f.cache.WindowCapOf(meta.Ino(ino)) }

func (f *FS) Downloading() int { return f.cache.Downloading() }

func (f *FS) FillBases(ctx context.Context) {
	var wg sync.WaitGroup
	f.edits.StartBases(ctx, func(fetch func()) { wg.Go(fetch) })
	wg.Wait()
}

func (f *FS) ChangeShown(change func(meta.Visible) error, items ...meta.Item) error {
	return f.shown.local(change, items...)
}

func (f *FS) WaitTold() { f.shown.telling.Wait() }

func (f *FS) LoadRights(ctx context.Context) { f.loadRights(ctx) }

func (f *FS) SyncOnce(ctx context.Context) int {
	var wg sync.WaitGroup
	n := f.startRound(ctx, emptyIdleUpload, &wg)
	wg.Wait()
	return n
}

func (f *FS) BeforeFetch(fn func()) { f.edits.BeforeFetch(fn) }

func (f *FS) Generation(ino meta.Ino) (uint64, error) { return f.cache.Generation(ino) }

func (f *FS) FetchIn(ctx context.Context, it meta.Item, gen uint64) (fetchErr, checkErr error) {
	file := f.edits.FileOf(it)
	file.Gen = gen
	return f.cache.Fetch(ctx, file, [][2]int64{{0, file.Size}}), f.cache.Check(ctx, file)
}

func (f *FS) CacheLimitChanged(limit int64, by string) error { return f.cacheLimitChanged(limit, by) }

func (f *FS) HoldPoll() (release func()) {
	f.pollMu.Lock()
	return f.pollMu.Unlock
}

func (f *FS) BeforeOpen(fn func()) { f.beforeOpen = fn }

func LinkPage(it meta.Item) []byte { return linkPage(it) }
