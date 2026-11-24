package testenv

import (
	"context"
	"encoding/binary"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/clock"
	"github.com/Ruslan-Linuxoid/FastGD/internal/config"
	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func Limits() drive.Limits {
	d := drive.DefaultLimits()
	return drive.Limits{Units: d.Units, UnitsWindow: d.UnitsWindow, RetryFor: 300 * time.Millisecond,
		IdleRead:  time.Second,
		PerSecond: 1e6}
}

func Epoch() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }

const (
	UID = 1234
	GID = 5678
)

type Env struct {
	T     testing.TB
	Emu   *emulator.Emulator
	Srv   *httptest.Server
	Clock *clock.Fake
	Drive *drive.Client
	Dir   string
	FS    *core.FS
	Log   *slog.Logger

	CacheSize int64

	TransferTime time.Duration

	Tune func(*core.Options)
	logs *testWriter
}

func New(tb testing.TB) *Env {
	tb.Helper()
	clk := clock.NewFake(Epoch())
	emu := emulator.New(emulator.WithClock(clk))
	srv := httptest.NewServer(emu)
	tb.Cleanup(srv.Close)
	return newEnv(tb, emu, clk, srv, srv.URL, srv.Client())
}

func InBubble(t *testing.T, test func(t *testing.T, env *Env)) {
	t.Helper()
	synctest.Test(t, func(t *testing.T) {
		clk := clock.NewFake(Epoch())
		emu := emulator.New(emulator.WithClock(clk))
		test(t, newEnv(t, emu, clk, nil, "http://emulator", &http.Client{Transport: emulator.InProcess(emu)}))
	})
}

func newEnv(
	tb testing.TB, emu *emulator.Emulator, clk *clock.Fake, srv *httptest.Server, url string, hc *http.Client,
) *Env {
	tb.Helper()
	logs := newTestWriter(tb)
	log := slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	cl, err := drive.New(context.Background(), drive.Options{BaseURL: url, HTTPClient: hc, Limits: Limits()})
	if err != nil {
		tb.Fatal(err)
	}
	return &Env{T: tb, Emu: emu, Srv: srv, Clock: clk, Drive: cl, Dir: tb.TempDir(), Log: log, CacheSize: 1 << 30,
		TransferTime: config.DefaultTransferTime, logs: logs}
}

func (e *Env) Logs(substr string) []string {
	e.logs.mu.Lock()
	defer e.logs.mu.Unlock()
	var out []string
	for _, l := range e.logs.lines {
		if strings.Contains(l, substr) {
			out = append(out, l)
		}
	}
	return out
}

func (e *Env) SetSpeed(s drive.Speed) {
	e.T.Helper()
	e.newClient(drive.Options{Limits: Limits(), Speed: s})
}

func (e *Env) Start() *core.FS {
	e.T.Helper()
	fs, err := e.Open()
	if err != nil {
		e.T.Fatal(err)
	}
	return fs
}

func (e *Env) Open() (*core.FS, error) {
	opt := core.Options{
		CacheDir: e.Dir,
		Drive:    e.Drive,
		Clock:    e.Clock,
		UID:      UID,
		GID:      GID,

		FileMode: 0o644,
		DirMode:  0o755,
		Log:      e.Log,

		CacheSize:     e.CacheSize,
		TransferTime:  e.TransferTime,
		PrefetchShare: config.DefaultPrefetchShare,
	}
	if e.Tune != nil {
		e.Tune(&opt)
	}
	fs, err := core.Open(context.Background(), opt)
	if err != nil {
		return nil, err
	}
	e.FS = fs
	e.T.Cleanup(func() { fs.Close() })
	return fs, nil
}

func (e *Env) Restart() *core.FS {
	e.T.Helper()
	if e.FS != nil {
		if err := e.FS.Close(); err != nil {
			e.T.Fatal(err)
		}
	}
	return e.Start()
}

func (e *Env) Resolve(path string) (core.Attr, error) {
	a, err := e.FS.GetAttr(uint64(meta.RootIno))
	if err != nil {
		return a, err
	}
	if top, ok := strings.CutPrefix(path, "/"); ok {
		path = top
	} else {
		path = meta.MyDriveName + "/" + path
	}
	for part := range strings.SplitSeq(path, "/") {
		if part == "" {
			continue
		}
		if a, err = e.FS.Lookup(a.Ino, part); err != nil {
			return a, err
		}
	}
	return a, nil
}

func (e *Env) MustResolve(path string) core.Attr {
	e.T.Helper()
	a, err := e.Resolve(path)
	if err != nil {
		e.T.Fatalf("resolve %q: %v", path, err)
	}
	return a
}

func (e *Env) Names(path string) []string {
	e.T.Helper()
	a := e.MustResolve(path)
	entries, err := e.FS.ReadDir(a.Ino)
	if err != nil {
		e.T.Fatalf("readdir %q: %v", path, err)
	}
	names := make([]string, len(entries))
	for i, en := range entries {
		names[i] = en.Name
	}
	return names
}

func RequireErrno(tb testing.TB, err error, want syscall.Errno) {
	tb.Helper()
	if err != want {
		tb.Fatalf("got error %v, want %v", err, want)
	}
}

type testWriter struct {
	mu    sync.Mutex
	t     testing.TB
	done  bool
	lines []string
}

func newTestWriter(tb testing.TB) *testWriter {
	tb.Helper()
	w := &testWriter{t: tb}
	tb.Cleanup(func() {
		w.mu.Lock()
		w.done = true
		w.mu.Unlock()
	})
	return w
}

func (w *testWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.done {
		line := strings.TrimRight(string(p), "\n")
		w.t.Log(line)
		w.lines = append(w.lines, line)
	}
	return len(p), nil
}

func RandomBytes(n int, seed uint64) []byte {
	var key [32]byte
	binary.LittleEndian.PutUint64(key[:], seed)
	b := make([]byte, n)

	_, _ = rand.NewChaCha8(key).Read(b)
	return b
}

func (e *Env) newClient(o drive.Options) {
	e.T.Helper()
	o.BaseURL, o.HTTPClient = e.Srv.URL, e.Srv.Client()
	cl, err := drive.New(context.Background(), o)
	if err != nil {
		e.T.Fatal(err)
	}
	e.Drive = cl
}
