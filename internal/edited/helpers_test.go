package edited

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/cache"
	"github.com/Ruslan-Linuxoid/FastGD/internal/clock"
	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func (d *inDrive) Get(_ context.Context, id string) (*drive.File, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.asked++
	if d.down {
		return nil, io.ErrUnexpectedEOF
	}
	return &drive.File{ID: id, Size: int64(len(d.content)), MD5: sumOf(d.content), HeadRevision: "r1"}, nil
}

func (d *inDrive) KeepRevision(context.Context, string, string, bool) error { return nil }

func (d *inDrive) StreamRevision(ctx context.Context, id, _ string, off, length int64, sink func([]byte) error) error {
	return d.stream(ctx, id, "", off, length, sink)
}

func (d *inDrive) stream(_ context.Context, _, _ string, off, length int64, sink func([]byte) error) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.downloads++
	if d.down {
		return io.ErrUnexpectedEOF
	}
	return sink(d.content[off : off+length])
}

func (d *inDrive) set(down bool) {
	d.mu.Lock()
	d.down = down
	d.mu.Unlock()
}

func (d *inDrive) counts() (downloads, asked int) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.downloads, d.asked
}

func sumOf(b []byte) string {
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}

type noDrive struct{ t *testing.T }

func (d noDrive) Get(context.Context, string) (*drive.File, error) {
	d.t.Error("Drive was asked for an item")
	return nil, drive.ErrNotFound
}

func (d noDrive) KeepRevision(context.Context, string, string, bool) error {
	d.t.Error("Drive was asked to mark a revision")
	return drive.ErrNoRevision
}

func (d noDrive) StreamRevision(context.Context, string, string, int64, int64, func([]byte) error) error {
	d.t.Error("Drive was asked for a revision")
	return drive.ErrNoRevision
}

type kept struct {
	*Files

	store *meta.Store
	clean *cache.Cache
}

func open(t *testing.T) kept {
	t.Helper()
	return openOver(t, t.TempDir(), noDrive{t}, nil, nil)
}

func openOver(t *testing.T, dir string, d Drive, source cache.Source, fault func(string, meta.Item) error) kept {
	t.Helper()
	store, err := meta.Open(filepath.Join(dir, "meta.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.SetState(meta.StateRootID, "root"); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.DiscardHandler)
	clean, err := cache.New(cache.Options{Dir: filepath.Join(dir, "content"), Records: store, Size: 1 << 30, Log: log,
		Clock: clock.Real{}, Source: source})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(clean.Close)
	m := Open(Options{
		Disk: clean, Store: store, Marks: store, Drive: d, Source: source, Clock: clock.Real{}, Log: log,
		UploadDue:    func(meta.Ino) time.Time { return time.Unix(0, 0) },
		Backoff:      func(int) time.Duration { return time.Hour },
		RecoverFault: fault,
	}, clean.SyncLocal)
	return kept{m, store, clean}
}

func (k kept) fromDrive(t *testing.T, d *inDrive) meta.Item {
	t.Helper()
	sum := sumOf(d.content)
	err := k.store.PutItems([]meta.Item{{ID: d.id, Kind: meta.KindFile, Name: "f.bin", Parent: "root",
		Size: int64(len(d.content)), MD5: sum, Rev: "r1", Version: 1}})
	if err != nil {
		t.Fatal(err)
	}
	it, err := k.store.ByID(d.id)
	if err != nil {
		t.Fatal(err)
	}
	return it
}

func (k kept) whole(t *testing.T, ino meta.Ino) []byte {
	t.Helper()
	it := k.item(t, ino)
	size, _, _ := k.Seen(ino)
	got := make([]byte, size)
	if n, err := k.Read(t.Context(), it, got, 0); err != nil || int64(n) != size {
		t.Fatalf("read %d of %d bytes: %v", n, size, err)
	}
	return got
}

func (k kept) created(t *testing.T, id, name string, content []byte) meta.Item {
	t.Helper()
	if err := k.store.AddIDs([]string{id}); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	it, err := k.store.Visible().Create(meta.Item{Kind: meta.KindFile, Name: name, Parent: "root", Mtime: now, Ctime: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := k.clean.CreateLocal(it.ID); err != nil {
		t.Fatal(err)
	}
	if err := k.Write(context.Background(), it, 0, content); err != nil {
		t.Fatal(err)
	}
	return k.item(t, it.Ino)
}

func (k kept) item(t *testing.T, ino meta.Ino) meta.Item {
	t.Helper()
	it, err := k.store.ByIno(ino)
	if err != nil {
		t.Fatal(err)
	}
	return it
}

func (k kept) uploaded(t *testing.T, it meta.Item) Outcome {
	t.Helper()
	st := k.State(it)
	gen, size, mtime := st.Snapshot()

	content, err := os.ReadFile(k.clean.Path(it.ID))
	if err != nil {
		t.Fatal(err)
	}
	sum := fmt.Sprintf("%x", md5.Sum(content[:size]))
	out, err := k.Record(st, Upload{
		Item: it, Gen: gen, Size: size, Remote: meta.Remote{MD5: sum, Version: 2, Rev: "r1"}, SentMD5: sum,
		SentMeta: meta.Sent{Name: it.Name, Parent: it.Parent, Mtime: mtime},
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func (m *Files) Read(ctx context.Context, it meta.Item, dest []byte, off int64) (n int, err error) {
	err = m.WithBase(ctx, it.Ino, func() (err error) {
		n, err = m.ReadLocal(ctx, m.Local(it), dest, off)
		return err
	})
	return n, err
}

func (m *Files) Persist(it meta.Item) error {
	st := m.Lock(it)
	defer st.Unlock()
	after := it.UploadAfter
	if st.writers == 0 {
		after = m.opt.UploadDue(it.Ino)
	}
	return m.persist(it.Ino, st, after)
}

func (m *Files) FillBases(ctx context.Context) {
	var wg sync.WaitGroup
	m.StartBases(ctx, func(fetch func()) { wg.Go(fetch) })
	wg.Wait()
}
