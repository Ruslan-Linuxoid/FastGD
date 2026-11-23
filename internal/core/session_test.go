package core_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestStoppedUploadGoesOnAfterRestart(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	smallSessions(env)
	env.Start()
	content := testenv.RandomBytes(sessionFrom+8<<20, 54)
	upToFirstChunk(t, env, content)

	env.Restart()
	held := env.Emu.SessionBytes()
	env.Emu.ResetRequests()
	uploadAll(t, env)
	u := uploadsSeen(t, env)
	if u.sessions != 0 || u.status == 0 || len(u.chunks) == 0 || u.chunks[0] != held {
		t.Fatalf("after the restart the upload went as %+v, want the session to go on from %d", u, held)
	}
	if f := inDrive(t, env, "f.bin"); md5Of(f.Content) != md5Of(content) {
		t.Fatal("the file went up different")
	}
}

func TestLostLastAnswerCompletesFromTheStatus(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	smallSessions(env)
	env.Start()
	content := testenv.RandomBytes(sessionFrom+6<<20, 60)
	writeFile(t, env, "", "f.bin", content)
	env.Emu.LoseUploadAnswers(1)
	uploadAll(t, env)
	u := uploadsSeen(t, env)
	if u.sessions != 1 || u.status == 0 {
		t.Fatalf("after a lost answer the upload went as %+v", u)
	}
	seen := map[int64]bool{}
	for _, off := range u.chunks {
		if seen[off] {
			t.Fatalf("the chunk at %d went twice: %+v", off, u)
		}
		seen[off] = true
	}
	if f := inDrive(t, env, "f.bin"); md5Of(f.Content) != md5Of(content) {
		t.Fatal("the file went up different")
	}
}

func TestSessionGoneMidwayUploadsAgain(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	smallSessions(env)
	env.Start()
	content := testenv.RandomBytes(sessionFrom+8<<20, 61)
	env.Emu.SetUploadRate(4 << 20)
	writeFile(t, env, "", "f.bin", content)
	env.Clock.Advance(20 * time.Minute)
	done := make(chan struct{})
	go func() {
		env.FS.SyncOnce(context.Background())
		close(done)
	}()
	waitUntil(t, func() bool { return env.Emu.SessionBytes() >= 1<<20 })
	env.Emu.ExpireUploadSessions()
	env.Emu.SetUploadRate(0)
	<-done

	if u := uploadsSeen(t, env); u.sessions != 2 {
		t.Fatalf("after the session was gone the upload went as %+v, want a second session", u)
	}
	if it := item(t, env, "f.bin"); it.Dirty || it.Attempts != 0 || it.LastError != "" {
		t.Fatalf("after the round the file is dirty %v, with %d failed tries and the error %q", it.Dirty, it.Attempts,
			it.LastError)
	}
	if f := inDrive(t, env, "f.bin"); md5Of(f.Content) != md5Of(content) {
		t.Fatal("the file went up different")
	}
}

func TestUploadGivesUpForTheRoundWhenDriveTakesNoByte(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	smallSessions(env)
	env.Start()
	content := testenv.RandomBytes(sessionFrom+2<<20, 62)
	writeFile(t, env, "", "f.bin", content)
	env.Emu.CutUploads(-1, 0)
	env.Clock.Advance(20 * time.Minute)
	env.FS.SyncOnce(context.Background())
	it := item(t, env, "f.bin")
	if !it.Dirty || !strings.Contains(it.LastError, "took no byte of 3 chunks in a row") {
		t.Fatalf("the file is dirty %v with the error %q", it.Dirty, it.LastError)
	}
	if u := uploadsSeen(t, env); len(u.chunks) != 3 {
		t.Fatalf("the round sent %d chunks, want 3: %+v", len(u.chunks), u)
	}
	ses, err := env.FS.Store().UploadSession(it.Ino)
	if err != nil || ses.Address == "" {
		t.Fatalf("the session is not kept for the next round: %v", err)
	}
	env.Emu.CutUploads(0, 0)
	env.Emu.ResetRequests()
	uploadAll(t, env)
	if u := uploadsSeen(t, env); u.sessions != 0 {
		t.Fatalf("the next round opened %d sessions, want the kept one to go on", u.sessions)
	}
	if f := inDrive(t, env, "f.bin"); md5Of(f.Content) != md5Of(content) {
		t.Fatal("the file went up different")
	}
}

func TestEmulatorTurnsAwayChunksDriveTurnsAway(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	ctx := context.Background()
	ids, err := env.Drive.GenerateIDs(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	const size = 1 << 20
	addr, err := env.Drive.StartCreate(ctx, ids[0], "f.bin", env.Emu.Root(), "application/octet-stream", time.Now(), size)
	if err != nil {
		t.Fatal(err)
	}
	put := func(from, to int64, body int) int {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodPut, addr, bytes.NewReader(make([]byte, body)))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", from, to-1, size))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := put(0, 100000, 100000); code != http.StatusBadRequest {
		t.Fatalf("a chunk that ends at 100000 got %d, want 400", code)
	}
	if code := put(0, 256<<10, 1000); code != http.StatusBadRequest {
		t.Fatalf("a chunk with a body shorter than its range got %d, want 400", code)
	}
	if code := put(0, 256<<10, 256<<10); code != 308 {
		t.Fatalf("a chunk of 256 KiB got %d, want 308", code)
	}
	if got := env.Emu.SessionBytes(); got != 256<<10 {
		t.Fatalf("the session holds %d bytes, want the one chunk that was taken", got)
	}
}

func TestExpiredSessionUploadsAgain(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	smallSessions(env)
	env.Start()
	content := testenv.RandomBytes(sessionFrom+8<<20, 56)
	upToFirstChunk(t, env, content)

	env.Restart()
	env.Emu.ExpireUploadSessions()
	env.Emu.ResetRequests()
	uploadAll(t, env)
	if u := uploadsSeen(t, env); u.sessions != 1 || !slices.Contains(u.chunks, 0) {
		t.Fatalf("after the session expired the upload went as %+v, want a new session", u)
	}
	if f := inDrive(t, env, "f.bin"); md5Of(f.Content) != md5Of(content) {
		t.Fatal("the file went up different")
	}
}

func TestChangedBigFileGoesUpInASession(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	smallSessions(env)
	env.Start()
	content := testenv.RandomBytes(sessionFrom+7<<20, 57)
	writeFile(t, env, "", "f.bin", content)
	uploadAll(t, env)
	env.Emu.ResetRequests()

	if err := env.FS.Truncate(context.Background(), env.MustResolve("f.bin").Ino, sessionFrom+6<<20); err != nil {
		t.Fatal(err)
	}
	uploadAll(t, env)
	if u := uploadsSeen(t, env); u.sessions != 1 {
		t.Fatalf("the new content went up as %+v, want one session", u)
	}
	if f := inDrive(t, env, "f.bin"); md5Of(f.Content) != md5Of(content[:sessionFrom+6<<20]) {
		t.Fatal("the new content went up different")
	}
}
