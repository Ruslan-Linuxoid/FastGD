package core_test

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestWriteAfterAnUploadThatOvertookTheOpenReachesDrive(t *testing.T) {
	t.Parallel()
	env, content := fromDrive(t, 64<<10, 800)
	fh := openWriter(t, env, "f.bin", 0)
	mustRead(t, env, fh, 0, len(content))
	write(t, env, fh, 0, []byte("first"))
	env.FS.Release(fh)
	copy(content, "first")
	ino := env.MustResolve("f.bin").Ino
	read := item(t, env, "f.bin")
	uploadAll(t, env)
	env.FS.Edits().AddWriter(read.Item)
	if err := env.FS.Truncate(context.Background(), ino, 1000); err != nil {
		t.Fatal(err)
	}
	env.FS.Edits().CloseWriter(read.Ino)
	if it := item(t, env, "f.bin"); !it.Dirty {
		t.Fatal("the change went into a file the database holds clean")
	}
	uploadAll(t, env)
	if f := inDrive(t, env, "f.bin"); !bytes.Equal(f.Content, content[:1000]) {
		t.Fatalf("Drive holds %d bytes, want the 1000 the file was cut to", len(f.Content))
	}
}

func TestWriteWhoseDownloadAnotherWriteStoppedGoesThrough(t *testing.T) {
	env := testenv.New(t)
	content := startWith(env, 256<<10, 810)
	a := openWriter(t, env, "f.bin", 0)
	b := openWriter(t, env, "f.bin", 0)
	env.Emu.SetMediaDelay(time.Second)
	wrote := make(chan error, 1)
	go func() {
		_, err := env.FS.Write(context.Background(), a, 10000, []byte("needs its page"))
		wrote <- err
	}()
	waitUntil(t, func() bool { return len(mediaRanges(env)) > 0 })
	whole := testenv.RandomBytes(4096, 811)
	write(t, env, b, 100<<10, whole)
	if err := <-wrote; err != nil {
		t.Fatalf("the write whose download was stopped got %v", err)
	}
	env.Emu.SetMediaDelay(0)
	copy(content[10000:], "needs its page")
	copy(content[100<<10:], whole)
	env.FS.Release(a)
	env.FS.Release(b)
	if got := readAll(t, env, "f.bin"); !bytes.Equal(got, content) {
		t.Fatal("the file reads different after the two writes")
	}
}

func TestReadWhoseDownloadAWriteStoppedGetsItsBytes(t *testing.T) {
	env := testenv.New(t)
	content := startWith(env, 256<<10, 820)
	r := openFile(t, env, "f.bin")
	w := openWriter(t, env, "f.bin", 0)
	env.Emu.SetMediaDelay(time.Second)
	type result struct {
		b   []byte
		err error
	}
	read := make(chan result, 1)
	go func() {
		b, err := tryRead(env, r, 0, 8192)
		read <- result{b, err}
	}()
	waitUntil(t, func() bool { return len(mediaRanges(env)) > 0 })
	whole := testenv.RandomBytes(4096, 821)
	write(t, env, w, 100<<10, whole)
	got := <-read
	if got.err != nil || !bytes.Equal(got.b, content[:8192]) {
		t.Fatalf("the read whose download a write stopped got %v", got.err)
	}
	env.FS.Release(w)
}

func TestOpenWriterDoesNotKeepTheSizeOfTheVersionBefore(t *testing.T) {
	t.Parallel()
	env, _ := fromDrive(t, 100, 830)
	fh := openWriter(t, env, "f.bin", 0)
	defer env.FS.Release(fh)
	id := item(t, env, "f.bin").ID
	changed := testenv.RandomBytes(200, 831)
	env.Emu.Update(id, func(f *emulator.File) { f.Content = changed })
	poll(t, env)
	if a := env.MustResolve("f.bin"); a.Size != 200 {
		t.Fatalf("stat shows %d bytes, the file has 200 now", a.Size)
	}
	if got := readAll(t, env, "f.bin"); !bytes.Equal(got, changed) {
		t.Fatalf("another handle reads %d bytes of the 200", len(got))
	}
}

func TestWriterHandleReadsWhatItWroteAfterAChangeInDrive(t *testing.T) {
	t.Parallel()
	env, _ := fromDrive(t, 64<<10, 840)
	fh := openWriter(t, env, "f.bin", 0)
	defer env.FS.Release(fh)
	mustRead(t, env, fh, 0, 4096)
	id := item(t, env, "f.bin").ID
	changed := testenv.RandomBytes(64<<10, 841)
	env.Emu.Update(id, func(f *emulator.File) { f.Content = changed })
	poll(t, env)
	write(t, env, fh, 100, []byte("mine"))
	copy(changed[100:], "mine")
	if got := mustRead(t, env, fh, 0, 64<<10); !bytes.Equal(got, changed) {
		t.Fatal("the handle does not read the file it wrote into")
	}
}

func TestNewFileIsNotDueBeforeItsWriterIsCounted(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	_, fh := createFile(t, env, "", "new.bin")
	defer env.FS.Release(fh)
	if it := item(t, env, "new.bin"); !it.UploadAfter.After(env.Clock.Now()) {
		t.Fatalf("the new file may be uploaded from %v, before the close of its writer; the clock shows %v", it.UploadAfter,
			env.Clock.Now())
	}
}

type slowChanges struct {
	next http.RoundTripper
	hold time.Duration
}

func (s slowChanges) RoundTrip(r *http.Request) (*http.Response, error) {
	if strings.HasSuffix(r.URL.Path, "/changes") {
		time.Sleep(s.hold)
	}
	return s.next.RoundTrip(r)
}

func TestSlowPollHoldsUpNoUpload(t *testing.T) {
	testenv.InBubble(t, func(t *testing.T, env *testenv.Env) {
		cl, err := drive.New(context.Background(), drive.Options{
			BaseURL: "http://emulator", Limits: testenv.Limits(),
			HTTPClient: &http.Client{Transport: slowChanges{emulator.InProcess(env.Emu), 5 * time.Minute}},
		})
		if err != nil {
			t.Fatal(err)
		}
		env.Drive = cl
		env.Start()
		ctx, stop := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			env.FS.Run(ctx)
			close(done)
		}()
		defer func() {
			stop()
			<-done
		}()
		writeFile(t, env, "", "f.txt", []byte("content"))
		env.Clock.Advance(time.Minute)
		time.Sleep(30 * time.Second)
		found := false
		for _, f := range env.Emu.Children(env.Emu.Root()) {
			found = found || (f.Name == "f.txt" && string(f.Content) == "content")
		}
		if !found {
			t.Fatal("with a poll of Changes under way for half a minute the file is not in Drive")
		}
	})
}

func TestWritePastTheEndOfAVersionThatBecameShorter(t *testing.T) {
	env, _ := fromDrive(t, 100<<10, 850)
	fh := openWriter(t, env, "f.bin", 0)
	id := item(t, env, "f.bin").ID
	changed := testenv.RandomBytes(40<<10, 851)
	env.Emu.SetMediaDelay(time.Second)
	wrote := make(chan error)
	go func() {
		_, err := env.FS.Write(context.Background(), fh, 90000, []byte("edit"))
		wrote <- err
	}()
	waitUntil(t, func() bool { return len(mediaRanges(env)) > 0 })
	env.Emu.Update(id, func(f *emulator.File) { f.Content = changed })
	poll(t, env)
	env.Emu.SetMediaDelay(0)
	if err := <-wrote; err != nil {
		t.Fatalf("the write past the end of the shorter version got %v", err)
	}
	env.FS.Release(fh)
	want := append(append([]byte(nil), changed...), make([]byte, 90000-len(changed))...)
	want = append(want, "edit"...)
	if got := readAll(t, env, "f.bin"); !bytes.Equal(got, want) {
		t.Fatalf("the file reads %d bytes, want the new version, zeros and the edit: %d", len(got), len(want))
	}
}
