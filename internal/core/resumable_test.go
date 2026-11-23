package core_test

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

type uploadRequests struct {
	sessions, status, simple int
	chunks                   []int64
	sizes                    []int64
}

func uploadsSeen(t *testing.T, env *testenv.Env) uploadRequests {
	t.Helper()
	var u uploadRequests
	for _, r := range env.Emu.Requests() {
		if !strings.HasPrefix(r.Path, "/upload/") {
			continue
		}
		switch {
		case r.Method == http.MethodPut && strings.HasPrefix(r.ContentRange, "bytes */"):
			u.status++
		case r.Method == http.MethodPut:
			var from, to, size int64
			if _, err := fmt.Sscanf(r.ContentRange, "bytes %d-%d/%d", &from, &to, &size); err != nil {
				t.Fatalf("chunk with Content-Range %q", r.ContentRange)
			}
			u.chunks = append(u.chunks, from)
			u.sizes = append(u.sizes, to-from+1)
		case r.Query.Get("uploadType") == "resumable":
			u.sessions++
		default:
			u.simple++
		}
	}
	return u
}

func TestOnlyFilesFrom100MiBGoInASession(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	small := testenv.RandomBytes(realSessionFrom-1, 50)
	writeFile(t, env, "", "small.bin", small)
	uploadAll(t, env)
	if u := uploadsSeen(t, env); u.simple != 1 || u.sessions != 0 {
		t.Fatalf("a file of a byte less than 100 MiB went up as %+v, want one simple upload", u)
	}
	if f := inDrive(t, env, "small.bin"); md5Of(f.Content) != md5Of(small) {
		t.Fatal("the file went up different")
	}

	env.Emu.ResetRequests()
	big := testenv.RandomBytes(realSessionFrom, 51)
	writeFile(t, env, "", "big.bin", big)
	uploadAll(t, env)
	if u := uploadsSeen(t, env); u.simple != 0 || u.sessions != 1 || len(u.chunks) == 0 {
		t.Fatalf("a file of 100 MiB went up as %+v, want one session", u)
	}
	if f := inDrive(t, env, "big.bin"); md5Of(f.Content) != md5Of(big) {
		t.Fatal("the file went up different")
	}
}

func TestCutSimpleUploadGoesAgainWhole(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	env.Start()
	content := testenv.RandomBytes(6<<20, 80)
	writeFile(t, env, "", "f.bin", content)
	env.Emu.CutSimpleUploads(1, 1<<20)
	uploadAll(t, env)
	if u := uploadsSeen(t, env); u.simple != 2 || u.sessions != 0 {
		t.Fatalf("a simple upload cut after 1 MiB went as %+v, want it twice whole", u)
	}
	if f := inDrive(t, env, "f.bin"); md5Of(f.Content) != md5Of(content) {
		t.Fatal("the file went up different")
	}

	env.Emu.ResetRequests()
	fh := openWriter(t, env, "f.bin", 0)
	write(t, env, fh, 3<<20, []byte("changed"))
	env.FS.Release(fh)
	copy(content[3<<20:], "changed")
	env.Emu.CutSimpleUploads(1, 2<<20)
	uploadAll(t, env)
	if u := uploadsSeen(t, env); u.simple != 2 || u.sessions != 0 {
		t.Fatalf("an update cut after 2 MiB went as %+v, want it twice whole", u)
	}
	if f := inDrive(t, env, "f.bin"); md5Of(f.Content) != md5Of(content) {
		t.Fatal("the changed file went up different")
	}
}

func TestChunksFollowTheUploadSpeed(t *testing.T) {
	env := testenv.New(t)
	env.TransferTime = time.Second
	env.Emu.WriteFile(env.Emu.Root(), "read.bin", testenv.RandomBytes(4<<20, 58))
	smallSessions(env)
	env.Start()

	env.Emu.SetMediaRate(64 << 20)
	readAll(t, env, "read.bin")

	env.Emu.SetUploadRate(38 << 20 / 10)
	content := testenv.RandomBytes(sessionFrom+8<<20, 52)
	writeFile(t, env, "", "f.bin", content)
	sizes := chunksUpTo(t, env, 5<<20)
	if len(sizes) < 3 || sizes[0] != 1<<20 {
		t.Fatalf("chunks of %v, want the first at 1 MiB before any measurement", sizes)
	}
	for _, n := range sizes[1:] {
		if n != 2<<20 {
			t.Fatalf("chunks of %v at 3.8 MiB/s, want 2 MiB after the first", sizes)
		}
	}
	env.Emu.SetUploadRate(0)
	uploadAll(t, env)
	if f := inDrive(t, env, "f.bin"); md5Of(f.Content) != md5Of(content) {
		t.Fatal("the file went up different")
	}
}

func TestChunksAddTheUploadLatency(t *testing.T) {
	env := testenv.New(t)
	env.TransferTime = 250 * time.Millisecond
	smallSessions(env)
	env.Start()

	env.Emu.SetUploadRate(6 << 20)
	env.Emu.SetUploadDelay(250 * time.Millisecond)
	content := testenv.RandomBytes(sessionFrom+8<<20, 59)
	writeFile(t, env, "", "f.bin", content)
	sizes := chunksUpTo(t, env, 5<<20)
	if len(sizes) < 3 || sizes[0] != 1<<20 {
		t.Fatalf("chunks of %v, want the first at 1 MiB before any measurement", sizes)
	}
	for _, n := range sizes[1:] {
		if n != 2<<20 {
			t.Fatalf("chunks of %v at 6 MiB/s and a latency of 250 ms, want 2 MiB after the first", sizes)
		}
	}
	env.Emu.SetUploadRate(0)
	env.Emu.SetUploadDelay(0)
	uploadAll(t, env)
	if f := inDrive(t, env, "f.bin"); md5Of(f.Content) != md5Of(content) {
		t.Fatal("the file went up different")
	}
}

func TestCutChunkGoesOnFromWhatDriveHas(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	smallSessions(env)
	env.Start()
	content := testenv.RandomBytes(sessionFrom+6<<20, 53)
	writeFile(t, env, "", "f.bin", content)
	env.Emu.CutUploads(1, 300<<10)
	uploadAll(t, env)
	u := uploadsSeen(t, env)
	if u.sessions != 1 || u.status == 0 || !slices.Contains(u.chunks, 300<<10) {
		t.Fatalf("after a cut at 300 KiB the upload went as %+v", u)
	}

	for i, from := range u.chunks {
		if end := from + u.sizes[i]; end != int64(len(content)) && end%(256<<10) != 0 {
			t.Fatalf("the chunk at %d ends at %d, not at a multiple of 256 KiB: %+v", from, end, u)
		}
	}
	if f := inDrive(t, env, "f.bin"); md5Of(f.Content) != md5Of(content) {
		t.Fatal("the file went up different")
	}
}

func TestSessionDriveRefusesForGoodStartsAgain(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	smallSessions(env)
	env.Start()
	content := testenv.RandomBytes(sessionFrom+6<<20, 71)
	writeFile(t, env, "", "f.bin", content)
	refused := false
	env.FS.BeforeChunk(func(off int64) {
		if off > 0 && !refused {
			refused = true
			env.Emu.FailRequests(1, 400, "badRequest", 0)
		}
	})
	env.Clock.Advance(20 * time.Minute)
	env.FS.SyncOnce(context.Background())
	if !refused {
		t.Fatal("scenario not built: no second chunk")
	}
	if u := uploadsSeen(t, env); u.sessions != 1 || u.status != 0 {
		t.Fatalf("after the refusal the round went on as %+v, want it to end there", u)
	}
	if ses, err := env.FS.Store().UploadSession(item(t, env, "f.bin").Ino); err != nil || ses.Address != "" {
		t.Fatalf("the refused session is kept: %+v, %v", ses, err)
	}
	uploadAll(t, env)
	if u := uploadsSeen(t, env); u.sessions != 2 {
		t.Fatalf("the upload went on as %+v, want a second session", u)
	}
	if f := inDrive(t, env, "f.bin"); md5Of(f.Content) != md5Of(content) {
		t.Fatal("the file went up different")
	}
}

func TestRateLimitOnAChunkKeepsTheSession(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)
	smallSessions(env)
	env.Start()
	content := testenv.RandomBytes(sessionFrom+6<<20, 72)
	writeFile(t, env, "", "f.bin", content)
	limited := false
	env.FS.BeforeChunk(func(off int64) {
		if off > 0 && !limited {
			limited = true
			env.Emu.FailRequests(1, 429, "rateLimitExceeded", 0)
		}
	})
	uploadAll(t, env)
	if u := uploadsSeen(t, env); !limited || u.sessions != 1 {
		t.Fatalf("after a rate limit on a chunk the upload went as %+v, want one session", u)
	}
	if f := inDrive(t, env, "f.bin"); md5Of(f.Content) != md5Of(content) {
		t.Fatal("the file went up different")
	}
}

func upToFirstChunk(t *testing.T, env *testenv.Env, content []byte) {
	t.Helper()
	env.Emu.SetUploadRate(4 << 20)
	writeFile(t, env, "", "f.bin", content)
	chunksUpTo(t, env, 1<<20)
	env.Emu.SetUploadRate(0)
}

func chunksUpTo(t *testing.T, env *testenv.Env, held int64) []int64 {
	t.Helper()
	env.Clock.Advance(20 * time.Minute)
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		env.FS.SyncOnce(ctx)
		close(done)
	}()
	waitUntil(t, func() bool { return env.Emu.SessionBytes() >= held })
	stop()
	<-done
	return uploadsSeen(t, env).sizes
}
