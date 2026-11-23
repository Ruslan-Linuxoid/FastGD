package core_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestAppendsDuringTheUploadOfTheVersionBefore(t *testing.T) {
	t.Parallel()

	env := testenv.New(t)
	smallSessions(env)
	content := startWith(env, sessionFrom+6<<20, 99)
	env.Emu.SetUploadRate(8 << 20)
	fh := openWriter(t, env, "f.bin", 0)
	want := append([]byte(nil), content...)
	more := testenv.RandomBytes(256<<10, 100)
	write(t, env, fh, int64(len(want)), more)
	want = append(want, more...)
	env.FS.Release(fh)
	env.Clock.Advance(20 * time.Minute)
	done := make(chan struct{})
	go func() {
		env.FS.SyncOnce(context.Background())
		close(done)
	}()

	for deadline := time.Now().Add(30 * time.Second); env.Emu.SessionBytes() == 0; {
		if time.Now().After(deadline) {
			t.Fatal("the upload sent nothing within 30 s")
		}
		time.Sleep(5 * time.Millisecond)
	}
	fh = openWriter(t, env, "f.bin", 0)
	for i := range 4 {
		chunk := testenv.RandomBytes(300<<10, uint64(101+i))
		write(t, env, fh, int64(len(want)), chunk)
		want = append(want, chunk...)
	}
	select {
	case <-done:
		t.Fatal("the upload of the version before ended before the appends")
	default:
	}
	env.FS.Release(fh)
	env.Emu.SetUploadRate(0)
	<-done
	uploadAll(t, env)
	if f := inDrive(t, env, "f.bin"); md5Of(f.Content) != md5Of(want) {
		t.Fatalf("Drive holds %d bytes, want the whole %d", len(f.Content), len(want))
	}
}

func requireRecordedLater(t *testing.T, writeAfter bool) {
	t.Helper()
	env, content := fromDrive(t, 3<<20, 79)
	fh := openWriter(t, env, "f.bin", 0)
	write(t, env, fh, 1<<20, []byte("edit"))

	if err := env.FS.Fsync(fh); err != nil {
		t.Fatal(err)
	}
	copy(content[1<<20:], "edit")
	fails := 1
	env.FS.BeforeRecord(func() error {
		if fails > 0 {
			fails--
			return errors.New("the database is busy")
		}
		return nil
	})
	env.Clock.Advance(20 * time.Minute)
	env.FS.SyncOnce(context.Background())
	if it := item(t, env, "f.bin"); !it.Dirty || !strings.Contains(it.LastError, "the database is busy") {
		t.Fatalf("after the record failed the file is dirty %v with the error %q", it.Dirty, it.LastError)
	}
	if f := inDrive(t, env, "f.bin"); !bytes.Equal(f.Content, content) {
		t.Fatal("scenario not built: Drive did not take the upload")
	}
	if writeAfter {
		write(t, env, fh, 2<<20, []byte("more"))
		copy(content[2<<20:], "more")
	}
	env.FS.Release(fh)
	uploadAll(t, env)
	requireRecordedAfterUpload(t, env, content, writeAfter)
}

func requireRecordedAfterUpload(t *testing.T, env *testenv.Env, content []byte, writeAfter bool) {
	t.Helper()
	it := item(t, env, "f.bin")
	if it.Dirty || it.LastError != "" || it.Base != 0 {
		t.Fatalf("the file is dirty %v, base %d, error %q", it.Dirty, it.Base, it.LastError)
	}
	f := inDrive(t, env, "f.bin")
	if !bytes.Equal(f.Content, content) || it.MD5 != md5Of(content) {
		t.Fatal("Drive and the database hold other content")
	}
	want := 1
	if writeAfter {
		want = 2
	}
	if u := uploadsSeen(t, env); u.simple != want {
		t.Fatalf("the content went up %d times, want %d", u.simple, want)
	}
}

func TestUploadTheDatabaseDidNotRecordIsRecordedLater(t *testing.T) {
	t.Parallel()
	for name, writeAfter := range map[string]bool{"no write since": false, "written since": true} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			requireRecordedLater(t, writeAfter)
		})
	}
}
