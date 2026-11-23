package core_test

import (
	"bytes"
	"context"
	"net/http"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func editedFromDrive(t *testing.T, env *testenv.Env, size int, off int64, seed uint64) []byte {
	t.Helper()
	content := testenv.RandomBytes(size, seed)
	env.Emu.WriteFile(env.Emu.Root(), "f.bin", content)
	env.Start()
	changedUnfetched(t, env)
	patch := testenv.RandomBytes(4096, seed+1)
	fh := openWriter(t, env, "f.bin", 0)
	write(t, env, fh, off, patch)
	env.FS.Release(fh)
	copy(content[off:], patch)
	env.Emu.ResetRequests()
	return content
}

func revisionCalls(env *testenv.Env) []string {
	var out []string
	for _, r := range env.Emu.Requests() {
		if strings.Contains(r.Path, "/revisions/") {
			call := r.Method
			if r.Query.Get("alt") == "media" {
				call += " media"
			}
			out = append(out, call)
		}
	}
	return out
}

func requireWholeOffline(t *testing.T, env *testenv.Env, want []byte) {
	t.Helper()
	env.Emu.SetOffline(true)
	defer env.Emu.SetOffline(false)
	if got := readAll(t, env, "f.bin"); !bytes.Equal(got, want) {
		t.Fatal("with Drive unreachable the edited file does not read as its Drive version with the edit")
	}
}

func TestBaseOfAFileChangedHereIsFetchedInTheBackground(t *testing.T) {
	testenv.InBubble(t, func(t *testing.T, env *testenv.Env) {
		content := testenv.RandomBytes(2<<20, 300)
		env.Emu.WriteFile(env.Emu.Root(), "f.bin", content)
		env.Start()
		changedUnfetched(t, env)

		env.Emu.SetMediaDelay(time.Second)
		fh := openWriter(t, env, "f.bin", 0)
		start := time.Now()
		patch := testenv.RandomBytes(4096, 301)
		write(t, env, fh, 1<<20, patch)
		if took := time.Since(start); took > 500*time.Millisecond {
			t.Fatalf("the write took %v", took)
		}
		copy(content[1<<20:], patch)
		env.Emu.SetMediaDelay(0)

		env.FS.FillBases(context.Background())
		requireWholeOffline(t, env, content)
		env.FS.Release(fh)
		if calls := revisionCalls(env); len(calls) != 0 {
			t.Fatalf("a base that Drive still holds was fetched with %v", calls)
		}
		if it := item(t, env, "f.bin"); it.Rev == "" || it.Rev != inDrive(t, env, "f.bin").HeadRevision {
			t.Fatalf("the item remembers the revision %q of its base", it.Rev)
		}
		uploadAll(t, env)
		if f := inDrive(t, env, "f.bin"); md5Of(f.Content) != md5Of(content) {
			t.Fatal("Drive holds other bytes than the edited file")
		}
	})
}

func TestBaseChangedInDriveComesFromItsRevision(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	want := editedFromDrive(t, env, 6<<20, 1<<20, 310)
	id := item(t, env, "f.bin").ID
	base := inDrive(t, env, "f.bin").HeadRevision
	changed := testenv.RandomBytes(5<<20, 312)
	env.Emu.Update(id, func(f *emulator.File) { f.Content = changed })
	env.FS.FillBases(context.Background())
	requireWholeOffline(t, env, want)
	if calls := revisionCalls(env); len(calls) == 0 || slices.ContainsFunc(calls, func(c string) bool {
		return c != "GET media"
	}) {
		t.Fatalf("the revision was fetched with %v, want downloads alone, with no mark", calls)
	}
	for _, r := range env.Emu.Revisions(id) {
		if r.Keep {
			t.Fatalf("revision %s stays marked Keep Forever", r.ID)
		}
	}
	if it := item(t, env, "f.bin"); it.Rev != base {
		t.Fatalf("the item names the revision %q, its base is %q", it.Rev, base)
	}

	env.Clock.Advance(20 * time.Minute)
	env.FS.SyncOnce(context.Background())
	if f := inDrive(t, env, "f.bin"); !bytes.Equal(f.Content, changed) {
		t.Fatal("the edit went over a version in Drive it is not based on")
	}
	requireShelved(t, env, "", "f", ".bin", want)
}

type onRequest struct {
	next  http.RoundTripper
	match func(*http.Request) bool
	fn    func()
}

func (h *onRequest) RoundTrip(r *http.Request) (*http.Response, error) {
	if h.match(r) {
		h.fn()
	}
	return h.next.RoundTrip(r)
}

func TestChangeInDriveDuringTheFetchMixesNoVersions(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	var armed bool
	var once sync.Once
	var id string
	tr := &onRequest{
		next:  env.Srv.Client().Transport,
		match: func(r *http.Request) bool { return armed && r.URL.Query().Get("alt") == "media" },

		fn: func() {
			once.Do(func() {
				env.Emu.Update(id, func(f *emulator.File) { f.Content = testenv.RandomBytes(6<<20, 322) })
			})
		},
	}
	cl, err := drive.New(context.Background(), drive.Options{BaseURL: env.Srv.URL, HTTPClient: &http.Client{Transport: tr},
		Limits: testenv.Limits()})
	if err != nil {
		t.Fatal(err)
	}
	env.Drive = cl
	want := editedFromDrive(t, env, 6<<20, 1<<20, 320)
	id = item(t, env, "f.bin").ID
	armed = true
	env.FS.FillBases(context.Background())
	requireWholeOffline(t, env, want)
}

func TestReadAndWriteWaitForTheBaseFromItsRevision(t *testing.T) {
	t.Parallel()
	for name, polled := range map[string]bool{"before the poll tells of the change": false, "after it": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			env := testenv.New(t)
			want := editedFromDrive(t, env, 256<<10, 8192, 330)
			id := item(t, env, "f.bin").ID
			env.Emu.Update(id, func(f *emulator.File) { f.Content = testenv.RandomBytes(256<<10, 332) })
			if polled {

				fh := openWriter(t, env, "f.bin", 0)
				defer env.FS.Release(fh)
				poll(t, env)
			}
			fh := openWriter(t, env, "f.bin", 0)
			if got := mustRead(t, env, fh, 100000, 4096); !bytes.Equal(got, want[100000:104096]) {
				t.Fatal("the read does not give the version the edit was made on")
			}

			write(t, env, fh, 200000, []byte("more"))
			copy(want[200000:], "more")
			env.FS.Release(fh)
			requireWholeOffline(t, env, want)
		})
	}
}

func TestReadThatWaitsForTheBaseFailsWithoutTheNetwork(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	editedFromDrive(t, env, 256<<10, 8192, 334)
	id := item(t, env, "f.bin").ID
	env.Emu.Update(id, func(f *emulator.File) { f.Content = testenv.RandomBytes(1000, 335) })
	fh := openWriter(t, env, "f.bin", 0)
	defer env.FS.Release(fh)
	poll(t, env)
	env.Emu.SetOffline(true)
	start := time.Now()
	if _, err := env.FS.Read(context.Background(), fh, 100000, make([]byte, 4096)); err != syscall.EIO {
		t.Fatalf("with no network the read got %v, want EIO", err)
	}
	if took := time.Since(start); took > 30*time.Second {
		t.Fatalf("the read failed after %v", took)
	}
}
