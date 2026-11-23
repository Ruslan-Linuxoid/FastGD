package core_test

import (
	"context"
	"testing"
	"testing/synctest"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestPollsKeepTheirPace(t *testing.T) {
	testenv.InBubble(t, func(t *testing.T, env *testenv.Env) {
		env.Start()
		env.Emu.ResetRequests()
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
		polls := func() int {
			n := 0
			for _, r := range env.Emu.Requests() {
				if r.Path == "/drive/v3/changes" {
					n++
				}
			}
			return n
		}

		polled := func(n int) {
			t.Helper()
			waitUntil(t, func() bool { return polls() == n })
			synctest.Wait()
		}
		polled(1)

		env.Clock.Advance(10*time.Second + 400*time.Millisecond)
		polled(2)
		env.Clock.Advance(9*time.Second + 600*time.Millisecond)
		polled(3)

		for i, gap := range []time.Duration{47 * time.Second, 15 * time.Second} {
			env.Clock.Advance(gap)
			polled(4 + 2*i)
			time.Sleep(3 * time.Second)
			env.Clock.Advance(9 * time.Second)
			time.Sleep(3 * time.Second)
			if n := polls(); n != 4+2*i {
				t.Fatalf("within 9 seconds of the poll after a gap of %v there were %d more polls", gap, n-4-2*i)
			}
			env.Clock.Advance(time.Second)
			polled(5 + 2*i)
		}
	})
}

func TestChangesArePolledEveryTenSecondsOfTheClock(t *testing.T) {
	testenv.InBubble(t, func(t *testing.T, env *testenv.Env) {
		env.Start()

		env.Emu.ResetRequests()
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
		polls := func() int {
			n := 0
			for _, r := range env.Emu.Requests() {
				if r.Path == "/drive/v3/changes" {
					n++
				}
			}
			return n
		}

		waitUntil(t, func() bool { return polls() == 1 })
		env.Clock.Advance(9*time.Second + 900*time.Millisecond)
		time.Sleep(1500 * time.Millisecond)
		if n := polls(); n != 1 {
			t.Fatalf("%d polls before 10 seconds passed", n)
		}
		env.Clock.Advance(100 * time.Millisecond)
		waitUntil(t, func() bool { return polls() == 2 })

		env.Clock.Advance(9*time.Second + 900*time.Millisecond)
		time.Sleep(1500 * time.Millisecond)
		if n := polls(); n != 2 {
			t.Fatalf("%d polls before 10 seconds passed since the second", n)
		}
		env.Clock.Advance(100 * time.Millisecond)
		waitUntil(t, func() bool { return polls() == 3 })
		for _, r := range env.Emu.Requests() {
			if r.Path == "/drive/v3/changes" && r.Query.Get("pageSize") != "1000" {
				t.Fatalf("a poll asked for pages of %q changes", r.Query.Get("pageSize"))
			}
		}
	})
}

func tickBothClocks(env *testenv.Env, d time.Duration) {
	for ; d > 0; d -= time.Second {
		env.Clock.Advance(time.Second)
		time.Sleep(time.Second)
	}
}

func TestAPollThatKeepsFailingHoldsUpNeitherTheQuotaNorTheProbe(t *testing.T) {
	testenv.InBubble(t, func(t *testing.T, env *testenv.Env) {
		env.Emu.SetQuota(15<<30, 4<<30)
		env.Start()
		env.Emu.FailPath("/drive/v3/changes", 503, "backendError")
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

		free := func() int64 {
			_, free := env.FS.StatFs()
			return (free + 1<<29) >> 30
		}

		pass := func(d time.Duration) {
			t.Helper()
			before := changesPages(env)
			tickBothClocks(env, d)
			if changesPages(env) == before {
				t.Fatal("scenario not built: the poll was not tried in this stretch")
			}
		}
		pass(3 * time.Second)
		if free() != 11 {
			t.Fatalf("three seconds after the start the mount shows %d GiB free, want the quota of Drive", free())
		}

		env.Emu.SetQuota(15<<30, 5<<30)
		writeFile(t, env, "", "f.txt", []byte("content"))
		pass(10 * time.Second)
		if it := item(t, env, "f.txt"); it.Dirty {
			t.Fatal("scenario not built: the file did not go to Drive")
		}
		if free() != 10 {
			t.Fatalf("ten seconds after an upload the mount shows %d GiB free, want the quota as it is now", free())
		}

		env.Emu.SetQuota(15<<30, 6<<30)
		pass(5*time.Minute + time.Second)
		if free() != 9 {
			t.Fatalf("five minutes on the mount shows %d GiB free, want the quota as it is now", free())
		}

		env.Emu.SetOffline(true)
		tickBothClocks(env, 5*time.Minute+time.Second)
		if !env.Drive.Offline() {
			t.Fatal("scenario not built: the client does not know the network is down")
		}
		env.Emu.SetOffline(false)

		tickBothClocks(env, 6*time.Second)
		if env.Drive.Offline() {
			t.Fatal("six seconds after the network came back the client still takes it for down")
		}
	})
}
