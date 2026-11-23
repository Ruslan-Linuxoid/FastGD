package core_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func driveContent(env *testenv.Env, name string) (content []byte, ok bool) {
	for _, f := range env.Emu.Children(env.Emu.Root()) {
		if f.Name == name {
			got, _ := env.Emu.Get(f.ID)
			return got.Content, true
		}
	}
	return nil, false
}

type bodyHook struct {
	io.ReadCloser

	n    int
	read int
	fn   func()
	once *sync.Once
}

func (b *bodyHook) Read(p []byte) (int, error) {
	k, err := b.ReadCloser.Read(p)
	b.read += k
	if b.read >= b.n {
		b.once.Do(b.fn)
	}
	return k, err
}

type hookTransport struct {
	next http.RoundTripper

	match func(*http.Request) bool
	n     int
	fn    func()
	once  sync.Once
}

func (h *hookTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Body != nil && h.match(r) {
		r2 := r.Clone(r.Context())
		r2.Body = &bodyHook{ReadCloser: r.Body, n: h.n, fn: h.fn, once: &h.once}
		r = r2
	}
	return h.next.RoundTrip(r)
}

func TestWriteDuringASimpleUploadNeverMixesVersions(t *testing.T) {
	t.Parallel()
	for name, update := range map[string]bool{"create": false, "update": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			requireWriteDuringSimpleUpload(t, update)
		})
	}
}

type failingReader struct {
	n   int
	err error
}

func (r *failingReader) Read(p []byte) (int, error) {
	if r.n == 0 {
		return 0, r.err
	}
	k := min(len(p), r.n)
	clear(p[:k])
	r.n -= k
	return k, nil
}

func TestContentThatFailsLeavesDriveAsItWas(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	ctx := context.Background()
	old := env.Emu.WriteFile(env.Emu.Root(), "old.bin", []byte("as it was"))
	ids, err := env.Drive.GenerateIDs(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	broken := errors.New("the content broke")
	tries := 0
	content := func() io.Reader {
		tries++
		return &failingReader{n: 300 << 10, err: broken}
	}
	_, err = env.Drive.CreateFile(ctx, ids[0], "new.bin", env.Emu.Root(), "application/octet-stream", time.Now(), 1<<20,
		content)
	if !errors.Is(err, broken) {
		t.Fatalf("the create got %v, want the error of its content", err)
	}
	_, err = env.Drive.UpdateContent(ctx, old, "application/octet-stream", time.Now(), 1<<20, content)
	if !errors.Is(err, broken) {
		t.Fatalf("the update got %v, want the error of its content", err)
	}
	if tries != 2 {
		t.Fatalf("the content was opened %d times, want once for each request", tries)
	}
	if _, ok := env.Emu.Get(ids[0]); ok {
		t.Fatal("Drive holds a file whose content broke")
	}
	if f, _ := env.Emu.Get(old); string(f.Content) != "as it was" {
		t.Fatalf("Drive holds %q in place of the old content", f.Content)
	}
}

func TestFilesAroundTheHeldTailGoUpWhole(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	for i, size := range []int{0, 1, 256 << 10, 256<<10 + 1, 512 << 10, 512<<10 + 1} {
		name := fmt.Sprintf("f%d.bin", size)
		content := testenv.RandomBytes(size, uint64(700+i))
		writeFile(t, env, "", name, content)
		uploadAll(t, env)
		if f := inDrive(t, env, name); !bytes.Equal(f.Content, content) {
			t.Fatalf("a file of %d bytes went up as %d bytes of other content", size, len(f.Content))
		}
	}
}
