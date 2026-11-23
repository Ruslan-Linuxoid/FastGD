package core_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestTypeOfAFileRenamedBeforeItsUploadIsOneInDriveAndHere(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	writeFile(t, env, "", "a.txt", []byte("text"))
	rename(t, env, "", "a.txt", "", "a.bin")
	uploadAll(t, env)
	if here, there := item(t, env, "a.bin").Mime, inDrive(t, env, "a.bin").MimeType; here != there ||
		here != "application/octet-stream" {
		t.Fatalf("the database has the type %q, Drive %q, want that of a.bin in both", here, there)
	}
}

func TestDamagedUploadsCountOnlyInARow(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	writeFile(t, env, "", "f.bin", testenv.RandomBytes(5000, 31))
	round := func() {
		env.Clock.Advance(20 * time.Minute)
		env.FS.SyncOnce(context.Background())
	}
	env.Emu.CorruptUploads(2)
	round()
	round()
	env.Emu.SetOffline(true)
	round()
	env.Emu.SetOffline(false)
	env.Emu.CorruptUploads(2)
	round()
	round()
	if it := item(t, env, "f.bin"); !it.Dirty || it.Attempts != 5 {
		t.Fatalf("scenario not built: the file is dirty %v after %d tries", it.Dirty, it.Attempts)
	}
	if lines := env.Logs("upload keeps arriving damaged"); len(lines) != 0 {
		t.Fatalf("four damaged uploads with another failure in the middle are logged as a row: %q", lines)
	}
	env.Emu.CorruptUploads(1)
	round()
	if lines := env.Logs("upload keeps arriving damaged"); len(lines) != 1 {
		t.Fatalf("three damaged uploads in a row gave %d lines in the log", len(lines))
	}
}

func requireUploadedClean(t *testing.T, env *testenv.Env, name, step string) {
	t.Helper()
	if it := item(t, env, name); it.Dirty || it.LastError != "" {
		t.Fatalf("after %s the file is dirty %v with the error %q", step, it.Dirty, it.LastError)
	}
}

func TestUploadAnswerWithoutMD5AsksDriveForIt(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Emu.LeaveMD5OutOfUploadAnswers(true)
	smallSessions(env)
	env.Start()

	round := func() {
		env.Clock.Advance(20 * time.Minute)
		env.FS.SyncOnce(context.Background())
	}
	content := testenv.RandomBytes(5000, 560)
	writeFile(t, env, "", "f.bin", content)
	env.Emu.ResetRequests()
	round()
	requireUploadedClean(t, env, "f.bin", "the upload")
	if sent, _ := uploads(env); sent != 1 {
		t.Fatalf("the content went to Drive %d times, want once", sent)
	}
	if f := inDrive(t, env, "f.bin"); !bytes.Equal(f.Content, content) {
		t.Fatal("Drive holds other content")
	}

	fh := openWriter(t, env, "f.bin", 0)
	write(t, env, fh, 100, []byte("changed"))
	env.FS.Release(fh)
	copy(content[100:], "changed")
	env.Emu.ResetRequests()
	round()
	requireUploadedClean(t, env, "f.bin", "the update")
	if sent, _ := uploads(env); sent != 1 {
		t.Fatalf("the new content went to Drive %d times, want once", sent)
	}
	if f := inDrive(t, env, "f.bin"); !bytes.Equal(f.Content, content) {
		t.Fatal("Drive holds other content after the update")
	}

	big := testenv.RandomBytes(sessionFrom, 561)
	writeFile(t, env, "", "big.bin", big)
	env.Emu.ResetRequests()
	round()
	requireUploadedClean(t, env, "big.bin", "the session")
	if u := uploadsSeen(t, env); u.simple != 0 || u.sessions != 1 {
		t.Fatalf("the file of 100 MiB went up as %+v, want one session", u)
	}
}
