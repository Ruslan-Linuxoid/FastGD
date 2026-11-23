package core_test

import (
	"bytes"
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

const (
	kib = 1 << 10
	mib = 1 << 20

	big = 4 * mib
)

func mediaRanges(env *testenv.Env) []string {
	var out []string
	for _, r := range env.Emu.Requests() {
		if r.Query.Get("alt") == "media" {
			out = append(out, r.Range)
		}
	}
	return out
}

func atRest(t *testing.T, env *testenv.Env) {
	t.Helper()
	waitUntil(t, func() bool { return env.FS.Downloading() == 0 })
}

func settled(t *testing.T, env *testenv.Env, want int) []string {
	t.Helper()
	waitUntil(t, func() bool { return len(mediaRanges(env)) >= want })
	atRest(t, env)
	got := mediaRanges(env)
	slices.Sort(got)
	return got
}

func rangeHeader(from, to int64) string { return fmt.Sprintf("bytes=%d-%d", from, to-1) }

func tryRead(env *testenv.Env, fh uint64, off int64, n int) ([]byte, error) {
	buf := make([]byte, n)
	got, err := env.FS.Read(context.Background(), fh, off, buf)
	if err != nil {
		return nil, fmt.Errorf("read %d at %d: %w", n, off, err)
	}
	return buf[:got], nil
}

func cachedBytes(ps []meta.Piece) int64 {
	var n int64
	for _, p := range ps {
		n += p.Len
	}
	return n
}

func requireNoOverlap(t *testing.T, ps []meta.Piece) {
	t.Helper()
	for i := 1; i < len(ps); i++ {
		if ps[i-1].Off+ps[i-1].Len > ps[i].Off {
			t.Fatalf("pieces overlap: %v and %v", ps[i-1], ps[i])
		}
	}
}

func contentPath(env *testenv.Env, it meta.Item) string { return contentFile(env, it.ID) }

func pieces(t *testing.T, env *testenv.Env, path string) []meta.Piece {
	t.Helper()
	ps, err := env.FS.Store().Pieces(meta.Ino(env.MustResolve(path).Ino))
	if err != nil {
		t.Fatal(err)
	}
	return ps
}

func TestConcurrentReadsNeverFetchAByteTwice(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	content := testenv.RandomBytes(big, 16)
	env.Emu.WriteFile(env.Emu.Root(), "f.bin", content)
	env.Start()
	env.Emu.SetMediaRate(4 * mib)
	env.Emu.ResetRequests()
	ino := env.MustResolve("f.bin").Ino

	errs := make(chan error, 8)
	for g := range 8 {
		go func() {
			fh, err := env.FS.Open(context.Background(), ino, syscall.O_RDONLY)
			if err != nil {
				errs <- err
				return
			}
			defer env.FS.Release(fh)
			r := rand.New(rand.NewPCG(uint64(g), 0))
			for range 20 {
				off := r.Int64N(big - 64*kib)
				buf := make([]byte, 1+r.IntN(64*kib))
				if _, err := env.FS.Read(context.Background(), fh, off, buf); err != nil {
					errs <- err
					return
				}
				if !bytes.Equal(buf, content[off:off+int64(len(buf))]) {
					errs <- fmt.Errorf("bytes at %d differ from Drive's", off)
					return
				}
			}
			errs <- nil
		}()
	}
	for range 8 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	atRest(t, env)

	requestSizes(t, env)
	ps := pieces(t, env, "f.bin")
	requireNoOverlap(t, ps)
}

func TestArrivedBytesAreServedBeforeTheWholePiece(t *testing.T) {
	env := testenv.New(t)
	content := testenv.RandomBytes(big, 17)
	env.Emu.WriteFile(env.Emu.Root(), "f.bin", content)
	env.Start()
	env.Emu.SetMediaRate(512 * kib)
	long := openFile(t, env, "f.bin")
	short := openFile(t, env, "f.bin")

	slow := make(chan time.Duration, 1)
	go func() {
		start := time.Now()
		if _, err := tryRead(env, long, 0, mib); err != nil {
			t.Error(err)
		}
		slow <- time.Since(start)
	}()
	waitUntil(t, func() bool { return len(mediaRanges(env)) > 0 })
	start := time.Now()
	if got := mustRead(t, env, short, 0, 4*kib); !bytes.Equal(got, content[:4*kib]) {
		t.Fatal("bytes differ from Drive's")
	}
	fast := time.Since(start)
	if whole := <-slow; fast > whole/4 {
		t.Fatalf("the head of a piece came after %v, the whole piece after %v", fast, whole)
	}
}

func TestCacheFileDeletedDuringADownloadServesNoHoles(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	content := testenv.RandomBytes(big, 18)
	env.Emu.WriteFile(env.Emu.Root(), "f.bin", content)
	env.Start()
	fh := openFile(t, env, "f.bin")
	mustRead(t, env, fh, 0, 4*kib)
	waitUntil(t, func() bool { return len(pieces(t, env, "f.bin")) == 1 })

	env.Emu.ResetRequests()
	env.Emu.SetMediaRate(512 * kib)
	done := make(chan []byte)
	go func() {
		got, err := tryRead(env, fh, 2*mib, 512*kib)
		if err != nil {
			t.Error(err)
		}
		done <- got
	}()
	waitUntil(t, func() bool { return len(mediaRanges(env)) > 0 })
	it := item(t, env, "f.bin")
	if err := os.Remove(contentPath(env, it.Item)); err != nil {
		t.Fatal(err)
	}
	if got := <-done; !bytes.Equal(got, content[2*mib:2*mib+512*kib]) {
		t.Fatal("the read under way differs from Drive's")
	}
	env.Emu.SetMediaRate(0)
	atRest(t, env)
	again := openFile(t, env, "f.bin")
	for _, off := range []int64{0, 2 * mib} {
		if got := mustRead(t, env, again, off, 8*kib); !bytes.Equal(got, content[off:off+8*kib]) {
			t.Fatalf("bytes at %d differ from Drive's after the cache file was deleted", off)
		}
	}
}
