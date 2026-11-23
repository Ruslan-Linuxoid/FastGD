package core_test

import (
	"bytes"
	"context"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestAFileWhoseRevisionsAreNotReadableReads(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	content := testenv.RandomBytes(256<<10, 1870)
	env.Emu.Put(emulator.File{ID: "shared000000", Name: "shared.bin", Parents: []string{env.Emu.Root()},
		Content: content, CannotReadRevisions: true})
	env.Start()
	env.Emu.ResetRequests()
	if got := readAll(t, env, "shared.bin"); !bytes.Equal(got, content) {
		t.Fatal("a file whose revisions the account may not read reads different")
	}
	for _, r := range env.Emu.Requests() {
		if strings.Contains(r.Path, "/revisions/") {
			t.Fatalf("the content came from %s, a revision the account may not read", r.Path)
		}
	}
}

func TestAReadInTheWindowOfAPollTakesTheNewVersion(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	var inWindow atomic.Pointer[func()]
	env.Tune = func(o *core.Options) {
		o.PollFault = func() error {
			if fn := inWindow.Swap(nil); fn != nil {
				(*fn)()
			}
			return nil
		}
	}
	id := env.Emu.WriteFile(env.Emu.Root(), "f.bin", testenv.RandomBytes(64<<10, 1871))
	env.Start()
	next := testenv.RandomBytes(64<<10, 1872)
	env.Emu.Update(id, func(f *emulator.File) { f.Content = next })
	ino := env.MustResolve("f.bin").Ino
	var got []byte
	done := make(chan error, 1)
	read := func() {
		go func() {
			fh, err := env.FS.Open(context.Background(), ino, syscall.O_RDONLY)
			if err != nil {
				done <- err
				return
			}
			defer env.FS.Release(fh)
			got = make([]byte, len(next))
			_, err = env.FS.Read(context.Background(), fh, 0, got)
			done <- err
		}()

		select {
		case err := <-done:
			done <- err
		case <-time.After(time.Second):
		}
	}
	inWindow.Store(&read)
	poll(t, env)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, next) {
		t.Fatal("the read in the window of the poll gave other bytes than the new version")
	}
	if !bytes.Equal(readAll(t, env, "f.bin"), next) {
		t.Fatal("after the poll the cache holds other bytes than the new version")
	}
}

func TestDriveRefusesARevisionTheAccountMayNotRead(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Emu.Put(emulator.File{ID: "shared000001", Name: "shared.bin", Parents: []string{env.Emu.Root()},
		Content: []byte("shared"), CannotReadRevisions: true})
	rev := env.Emu.Revisions("shared000001")[0].ID
	err := env.Drive.StreamRevision(context.Background(), "shared000001", rev, 0, 6, func([]byte) error { return nil })
	if err == nil || !drive.IsRefusal(err) {
		t.Fatalf("the revision of a file whose revisions the account may not read gives %v", err)
	}
}

func inPollWindow(env *testenv.Env) func(fn func()) (wait func()) {
	var next atomic.Pointer[func()]
	env.Tune = func(o *core.Options) {
		o.PollFault = func() error {
			if fn := next.Swap(nil); fn != nil {
				(*fn)()
			}
			return nil
		}
	}
	return func(fn func()) func() {
		done := make(chan struct{})
		run := func() {
			go func() {
				fn()
				close(done)
			}()
			select {
			case <-done:
			case <-time.After(time.Second):
			}
		}
		next.Store(&run)
		return func() { <-done }
	}
}

func TestAnOpenInTheWindowOfAPollReadsTheNewVersion(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	window := inPollWindow(env)
	id := env.Emu.WriteFile(env.Emu.Root(), "f.bin", testenv.RandomBytes(64<<10, 1929))
	env.Start()
	next := testenv.RandomBytes(96<<10, 1930)
	env.Emu.Update(id, func(f *emulator.File) { f.Content = next })
	ino := env.MustResolve("f.bin").Ino
	var fh uint64
	var err error
	wait := window(func() { fh, err = env.FS.Open(context.Background(), ino, syscall.O_RDONLY) })
	poll(t, env)
	wait()
	if err != nil {
		t.Fatal(err)
	}
	defer env.FS.Release(fh)
	if got := mustRead(t, env, fh, 0, len(next)); !bytes.Equal(got, next) {
		t.Fatal("the file opened in the window of the poll reads other bytes than the new version")
	}
}

func TestAReadOfAWriterInTheWindowOfAPollTakesTheNewVersion(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	window := inPollWindow(env)
	id := env.Emu.WriteFile(env.Emu.Root(), "f.bin", testenv.RandomBytes(64<<10, 1931))
	env.Start()
	fh := openWriter(t, env, "f.bin", 0)
	defer env.FS.Release(fh)
	next := testenv.RandomBytes(64<<10, 1932)
	env.Emu.Update(id, func(f *emulator.File) { f.Content = next })
	got := make([]byte, len(next))
	var err error
	wait := window(func() { _, err = env.FS.Read(context.Background(), fh, 0, got) })
	poll(t, env)
	wait()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, next) {
		t.Fatal("the read in the window of the poll gave other bytes than the new version")
	}
}

func TestAFirstWriteInTheWindowOfAPollBuildsOnTheNewVersion(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	window := inPollWindow(env)
	id := env.Emu.WriteFile(env.Emu.Root(), "f.bin", testenv.RandomBytes(4<<12, 1933))
	env.Start()
	next := testenv.RandomBytes(4<<12, 1934)
	env.Emu.Update(id, func(f *emulator.File) { f.Content = next })
	ino := env.MustResolve("f.bin").Ino
	data := testenv.RandomBytes(1<<12, 1935)
	var err error
	wait := window(func() {
		var fh uint64
		if fh, err = env.FS.Open(context.Background(), ino, syscall.O_WRONLY); err != nil {
			return
		}
		defer env.FS.Release(fh)
		_, err = env.FS.Write(context.Background(), fh, 1<<12, data)
	})
	poll(t, env)
	wait()
	if err != nil {
		t.Fatal(err)
	}
	want := bytes.Clone(next)
	copy(want[1<<12:], data)
	if !bytes.Equal(readAll(t, env, "f.bin"), want) {
		t.Fatal("the write in the window of the poll built on other bytes than the new version")
	}
}

func TestTheRightToReadRevisionsIsAskedWithTheRights(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Emu.WriteFile(env.Emu.Root(), "f.bin", []byte("asked"))
	env.Start()
	writeFile(t, env, "", "made.bin", []byte("made here"))
	uploadAll(t, env)
	poll(t, env)
	asked := 0
	for _, r := range env.Emu.Requests() {
		fields := r.Query.Get("fields")
		if !strings.Contains(fields, "capabilities(") {
			continue
		}
		asked++
		if !strings.Contains(fields, "canReadRevisions") {
			t.Fatalf("%s %s asks for rights without canReadRevisions: %s", r.Method, r.Path, fields)
		}
	}
	if asked == 0 {
		t.Fatal("scenario not built: no request asked for the rights of items")
	}
}

func TestAnOpenToCutInTheWindowOfAPollCutsTheNewVersion(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	window := inPollWindow(env)
	id := env.Emu.WriteFile(env.Emu.Root(), "f.bin", nil)
	env.Start()
	env.Emu.Update(id, func(f *emulator.File) { f.Content = testenv.RandomBytes(64<<10, 1939) })
	ino := env.MustResolve("f.bin").Ino
	var err error
	wait := window(func() {
		var fh uint64
		if fh, err = env.FS.Open(context.Background(), ino, syscall.O_WRONLY|syscall.O_TRUNC); err == nil {
			env.FS.Release(fh)
		}
	})
	poll(t, env)
	wait()
	if err != nil {
		t.Fatal(err)
	}
	if got := readAll(t, env, "f.bin"); len(got) != 0 {
		t.Fatalf("the file opened to be cut in the window of the poll holds %d bytes", len(got))
	}
}
