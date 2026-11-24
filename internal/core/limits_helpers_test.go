package core_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func longestLimitBackoff(n int) (d time.Duration) {
	for range 200 {
		d = max(d, core.LimitBackoff(n))
	}
	return d
}

func requireLimitBackoffDoubles(t *testing.T) {
	t.Helper()

	if d := longestLimitBackoff(1); d < 66*time.Second || d > 72*time.Second {
		t.Fatalf("the first refused upload waits up to %v, want a minute and its spread", d)
	}
	for n := 1; n < 6; n++ {
		if a, b := longestLimitBackoff(n), longestLimitBackoff(n+1); b < a*19/10 || b > a*21/10 {
			t.Fatalf("attempt %d waits up to %v and attempt %d up to %v, want twice as long", n, a, n+1, b)
		}
	}
}

func requireChunkRefusalShown(t *testing.T, env *testenv.Env, logged string) {
	t.Helper()
	if u := uploadsSeen(t, env); len(u.chunks) != 1 || u.status != 0 {
		t.Fatalf("after the refusal of a chunk the upload went on as %+v, want one chunk and no question", u)
	}
	if len(env.Logs("Drive takes no more uploads")) == 0 || len(env.Logs(logged)) == 0 {
		t.Fatalf("the refusal by %q is not in the log", logged)
	}
	requireReadOnly(t, env, 1)
	st := readStatus(t, env)
	if len(st.Failing) != 1 || st.Failing[0].Path != "My Drive/big.bin" ||
		!strings.Contains(st.Failing[0].LastError, logged) {
		t.Fatalf("status shows the failing upload as %+v", st.Failing)
	}
}

func requireUploadResumesAfterRefusal(t *testing.T, env *testenv.Env, content []byte) {
	t.Helper()
	env.Emu.ResetRequests()
	uploadAll(t, env)
	if u := uploadsSeen(t, env); u.sessions != 0 {
		t.Fatalf("after the refusal lifted the upload went as %+v, want the session it had", u)
	}
	if f := inDrive(t, env, "big.bin"); md5Of(f.Content) != md5Of(content) {
		t.Fatal("after the refusal lifted the file went up different")
	}
}

func refuseChunk(t *testing.T, env *testenv.Env, refuse func(on bool)) []byte {
	t.Helper()
	smallSessions(env)
	env.Start()
	content := testenv.RandomBytes(sessionFrom+mib, 95)
	writeFile(t, env, "", "big.bin", content)
	env.Emu.RefuseUploadsOnChunks(true)
	refuse(true)
	env.Emu.ResetRequests()
	env.Clock.Advance(20 * time.Minute)
	env.FS.SyncOnce(context.Background())
	return content
}
