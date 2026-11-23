package core_test

import (
	"bytes"
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func goneMidRead(t *testing.T, env *testenv.Env, seed uint64) []byte {
	t.Helper()
	it := item(t, env, "f.bin")
	next := testenv.RandomBytes(mixedSize, seed)
	env.Emu.CutMedia(1, mixedSize/2)
	env.Emu.OnMediaCut(func() {
		env.Emu.OnMediaCut(nil)
		env.Emu.Update(it.ID, func(f *emulator.File) { f.Content = next })
		env.Emu.DeleteRevision(it.ID, it.Rev)
	})
	if _, err := tryRead(env, openFile(t, env, "f.bin"), 0, mixedSize); err == nil {
		t.Fatal("scenario not built: the read whose revision went passed")
	}
	return next
}

func gets(env *testenv.Env, id string) int {
	n := 0
	for _, r := range env.Emu.Requests() {
		if r.Method == http.MethodGet && r.Path == "/drive/v3/files/"+id && r.Query.Get("alt") != "media" {
			n++
		}
	}
	return n
}

func TestRunReadsAnewAFileWhoseRevisionWent(t *testing.T) {
	testenv.InBubble(t, func(t *testing.T, env *testenv.Env) {
		env.Emu.WriteFile(env.Emu.Root(), "f.bin", testenv.RandomBytes(mixedSize, 1880))
		env.Start()
		ctx, stop := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			env.FS.Run(ctx)
			close(done)
		}()
		t.Cleanup(func() {
			stop()
			<-done
		})
		synctest.Wait()
		next := goneMidRead(t, env, 1881)
		time.Sleep(2 * time.Second)
		synctest.Wait()
		if item(t, env, "f.bin").MD5 != md5Of(next) {
			t.Fatal("two seconds after the revision went the item does not know the new version")
		}
	})
}

func TestAReadingAnewTheNetworkFailedGoesAgain(t *testing.T) {
	t.Parallel()
	env, _ := fromDrive(t, mixedSize, 1882)
	next := goneMidRead(t, env, 1883)
	env.Emu.SetOffline(true)
	env.FS.ReadGone(context.Background())
	env.Emu.SetOffline(false)
	if item(t, env, "f.bin").MD5 == md5Of(next) {
		t.Fatal("scenario not built: the reading anew passed with the network down")
	}
	env.Emu.ResetRequests()
	env.FS.ReadGone(context.Background())
	if n := gets(env, item(t, env, "f.bin").ID); n != 0 {
		t.Fatalf("the reading anew went again %d times before its wait", n)
	}
	env.Clock.Advance(time.Second)
	env.FS.ReadGone(context.Background())
	if item(t, env, "f.bin").MD5 != md5Of(next) {
		t.Fatal("the reading anew the network failed did not go again")
	}
	if got := readAll(t, env, "f.bin"); !bytes.Equal(got, next) {
		t.Fatal("after the reading anew the file reads other bytes than the new version")
	}
}

func TestAReadingAnewWaitsAsLongAsDriveAsked(t *testing.T) {
	t.Parallel()
	env, _ := fromDrive(t, mixedSize, 1936)
	next := goneMidRead(t, env, 1937)
	env.Emu.FailRequests(1<<20, 503, "backendError", time.Minute)
	env.FS.ReadGone(context.Background())
	env.Emu.FailRequests(0, 0, "", 0)
	if item(t, env, "f.bin").MD5 == md5Of(next) {
		t.Fatal("scenario not built: the reading anew passed with Drive turning it down")
	}
	env.Emu.ResetRequests()
	env.Clock.Advance(30 * time.Second)
	env.FS.ReadGone(context.Background())
	if n := gets(env, item(t, env, "f.bin").ID); n != 0 {
		t.Fatalf("the reading anew went again %d times before the wait Drive asked", n)
	}
	env.Clock.Advance(30 * time.Second)
	env.FS.ReadGone(context.Background())
	if item(t, env, "f.bin").MD5 != md5Of(next) {
		t.Fatal("the reading anew did not go again after the wait Drive asked")
	}
}

func TestAHeadRevisionDriveDoesNotGiveIsToldOnce(t *testing.T) {
	t.Parallel()
	env, content := fromDrive(t, 64<<10, 1884)
	it := item(t, env, "f.bin")
	env.Emu.DeleteRevision(it.ID, it.Rev)
	if _, err := tryRead(env, openFile(t, env, "f.bin"), 0, 64<<10); err == nil {
		t.Fatal("scenario not built: a read of a revision Drive does not give passed")
	}
	env.FS.ReadGone(context.Background())
	env.Emu.ResetRequests()
	if got := readAll(t, env, "f.bin"); !bytes.Equal(got, content) {
		t.Fatal("a file whose head revision Drive does not give reads other bytes")
	}
	env.FS.ReadGone(context.Background())
	if n := gets(env, it.ID); n != 0 {
		t.Fatalf("a read of the head revision Drive does not give sent %d readings anew", n)
	}
	if n := len(env.Logs("Drive names as the head of a file a revision it does not give")); n != 1 {
		t.Fatalf("the log tells of the revision Drive does not give %d times, want once", n)
	}
}

func TestDriveRefusesTheRevisionOfAFileNotToBeDownloaded(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Emu.Put(emulator.File{ID: "fixed0000001", Name: "fixed.bin", Parents: []string{env.Emu.Root()},
		Content: []byte("fixed"), CannotDownload: true})
	rev := env.Emu.Revisions("fixed0000001")[0].ID
	err := env.Drive.StreamRevision(context.Background(), "fixed0000001", rev, 0, 5, func([]byte) error { return nil })
	if err == nil || !drive.IsRefusal(err) {
		t.Fatalf("the revision of a file not to be downloaded gives %v", err)
	}
}

func TestARevisionDriveRefusesTheAccountReadsByTheFileID(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	content := testenv.RandomBytes(4<<20, 1938)
	env.Emu.Put(emulator.File{ID: "shared000002", Name: "shared.bin", Parents: []string{env.Emu.Root()},
		Content: content, CannotReadRevisions: true})
	env.Start()
	if err := env.FS.Close(); err != nil {
		t.Fatal(err)
	}
	env.FS = nil

	rev := env.Emu.Revisions("shared000002")[0].ID
	if err := execSQL(filepath.Join(env.Dir, "meta.db"),
		`UPDATE items SET rev = '`+rev+`' WHERE id = 'shared000002'`); err != nil {
		t.Fatal(err)
	}
	env.Tune = func(o *core.Options) { o.FieldsMark = "another set" }
	env.Emu.SetOffline(true)
	env.Start()
	env.Emu.SetOffline(false)
	env.Emu.ResetRequests()
	fh := openFile(t, env, "shared.bin")
	defer env.FS.Release(fh)
	for _, off := range []int{0, len(content) - 4096} {
		if got := mustRead(t, env, fh, int64(off), 4096); !bytes.Equal(got, content[off:off+4096]) {
			t.Fatalf("a file whose revision Drive refuses the account reads other bytes at %d", off)
		}
	}
	revs := 0
	for _, r := range env.Emu.Requests() {
		if strings.Contains(r.Path, "/revisions/") {
			revs++
		}
	}
	if n := len(mediaRanges(env)); revs != 1 || n < 3 {
		t.Fatalf("%d of %d downloads asked for the revision, want one of three or more", revs, n)
	}
	if n := len(env.Logs("Drive refuses the account a revision of a file")); n != 1 {
		t.Fatalf("the log told of the refused revision %d times, want once", n)
	}
}

func TestARevisionRefusedMidwayGoesByTheFileIDAtTheNextDownload(t *testing.T) {
	t.Parallel()
	env, content := fromDrive(t, 1<<20, 1940)
	id := item(t, env, "f.bin").ID
	env.Emu.CutMedia(1, 100<<10)
	env.Emu.OnMediaCut(func() {
		env.Emu.Update(id, func(f *emulator.File) { f.CannotReadRevisions = true })
	})
	fh := openFile(t, env, "f.bin")
	defer env.FS.Release(fh)
	env.Emu.ResetRequests()
	_, _ = tryRead(env, fh, 0, 1<<20)

	if got := mediaRanges(env); len(got) != 2 {
		t.Fatalf("the read asked Drive for %v, want the revision and its rest after the cut", got)
	}
	if got := readAll(t, env, "f.bin"); !bytes.Equal(got, content) {
		t.Fatal("after a revision refused midway the file reads other bytes")
	}
}
