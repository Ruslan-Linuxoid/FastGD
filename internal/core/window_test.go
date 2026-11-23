package core_test

import (
	"cmp"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func requestSizes(t *testing.T, env *testenv.Env) []int64 {
	t.Helper()
	type r struct{ from, to int64 }
	ranges := mediaRanges(env)
	rs := make([]r, 0, len(ranges))
	for _, h := range ranges {
		var x r
		if _, err := fmt.Sscanf(h, "bytes=%d-%d", &x.from, &x.to); err != nil {
			t.Fatalf("range %q: %v", h, err)
		}
		rs = append(rs, x)
	}
	slices.SortFunc(rs, func(a, b r) int { return cmp.Compare(a.from, b.from) })
	sizes := make([]int64, len(rs))
	for i, x := range rs {
		if i > 0 && rs[i-1].to >= x.from {
			t.Fatalf("downloads overlap: %v and %v", rs[i-1], x)
		}
		sizes[i] = x.to - x.from + 1
	}
	return sizes
}

func TestSequentialReadStaysUnderTheCap(t *testing.T) {
	t.Parallel()
	env := testenv.New(t)

	env.CacheSize = 8 * mib
	content := testenv.RandomBytes(big, 15)
	env.Emu.WriteFile(env.Emu.Root(), "video.bin", content)
	env.Start()
	fh := openFile(t, env, "video.bin")

	env.Emu.ResetRequests()
	for off := int64(0); off < big; off += 64 * kib {
		mustRead(t, env, fh, off, 64*kib)
	}

	want := []int64{64 * kib, 64 * kib, 64 * kib, 256 * kib, 576 * kib, mib, mib, mib}
	if got := requestSizes(t, env); !slices.Equal(got, want) {
		t.Fatalf("under a 1 MiB cap the requests are %v, want %v", got, want)
	}
}

func TestWindowCapFollowsTheNetwork(t *testing.T) {
	env := testenv.New(t)
	env.Emu.WriteFile(env.Emu.Root(), "f.bin", testenv.RandomBytes(big, 6))
	env.Start()
	if c := env.FS.WindowCap(); c != mib {
		t.Fatalf("before any measurement the cap is %d, want 1 MiB", c)
	}

	env.Emu.SetMediaRate(512 * kib)
	fh := openFile(t, env, "f.bin")
	mustRead(t, env, fh, 0, 4*kib)
	mustRead(t, env, fh, mib, 256*kib)
	mustRead(t, env, fh, 2*mib, 256*kib)
	if c := env.FS.WindowCap(); c != 4*mib {
		t.Fatalf("at 512 KiB/s the cap is %d, want 4 MiB", c)
	}
}

func TestWindowCapFollowsTheLinkTwoDownloadsShare(t *testing.T) {
	shared := testenv.New(t)
	shared.Emu.WriteFile(shared.Emu.Root(), "a.bin", testenv.RandomBytes(big, 19))
	shared.Emu.WriteFile(shared.Emu.Root(), "b.bin", testenv.RandomBytes(big, 20))
	shared.Start()
	shared.Emu.SetMediaRate(512 * kib)
	ah, bh := openFile(t, shared, "a.bin"), openFile(t, shared, "b.bin")
	read := make(chan error)
	for _, fh := range []uint64{ah, bh} {
		go func() {
			_, err := tryRead(shared, fh, mib, 256*kib)
			if err == nil {
				_, err = tryRead(shared, fh, 2*mib, 256*kib)
			}
			read <- err
		}()
	}
	for range 2 {
		if err := <-read; err != nil {
			t.Fatal(err)
		}
	}
	if c := shared.FS.WindowCap(); c != 4*mib {
		t.Fatalf("two downloads sharing 512 KiB/s give a cap of %d, want 4 MiB", c)
	}
}

func TestWindowIsDividedAmongTheFilesDownloadedAtOnce(t *testing.T) {
	env := testenv.New(t)
	names := []string{"a.bin", "b.bin", "c.bin"}
	for i, name := range names {
		env.Emu.WriteFile(env.Emu.Root(), name, testenv.RandomBytes(big, uint64(30+i)))
	}
	env.Start()

	env.Emu.SetMediaRate(512 * kib)
	fh := openFile(t, env, "a.bin")
	mustRead(t, env, fh, 0, 4*kib)
	mustRead(t, env, fh, mib, 256*kib)
	mustRead(t, env, fh, 2*mib, 256*kib)
	atRest(t, env)
	whole := env.FS.WindowCap()
	a := env.MustResolve("a.bin").Ino
	if whole != 4*mib || env.FS.WindowCapOf(a) != whole {
		t.Fatalf("a file read alone has a window of %d, the cap is %d, want 4 MiB for both", env.FS.WindowCapOf(a), whole)
	}

	env.Emu.StallMedia(1000)
	read := make(chan struct{})
	for _, name := range names {
		fh := openFile(t, env, name)
		go func() {
			_, _ = tryRead(env, fh, 3*mib, 256*kib)
			read <- struct{}{}
		}()
	}

	third := whole / 3 / 4096 * 4096
	windows := func() []int64 {
		out := make([]int64, 0, len(names))
		for _, name := range names {
			out = append(out, env.FS.WindowCapOf(env.MustResolve(name).Ino))
		}
		return out
	}
	deadline := time.Now().Add(5 * time.Second)
	for !slices.Equal(windows(), []int64{third, third, third}) {
		if time.Now().After(deadline) {
			t.Fatalf("with three files downloaded at once the windows are %v, want a third of %d each: %d", windows(), whole,
				third)
		}
		time.Sleep(5 * time.Millisecond)
	}
	env.Emu.StallMedia(-1)
	for range names {
		<-read
	}
}

func TestWindowCapIsAShareOfTheCache(t *testing.T) {
	small := testenv.New(t)
	small.CacheSize = 16 * mib
	small.Emu.WriteFile(small.Emu.Root(), "f.bin", testenv.RandomBytes(big, 7))
	small.Start()
	small.Emu.SetMediaRate(512 * kib)
	sh := openFile(t, small, "f.bin")
	mustRead(t, small, sh, mib, 256*kib)
	mustRead(t, small, sh, 2*mib, 256*kib)
	if c := small.FS.WindowCap(); c != 2*mib {
		t.Fatalf("with a 16 MiB cache the cap is %d, want an eighth, 2 MiB", c)
	}
}

func TestWindowCapAddsTheLatency(t *testing.T) {
	env := testenv.New(t)
	env.TransferTime = 500 * time.Millisecond
	env.Emu.WriteFile(env.Emu.Root(), "f.bin", testenv.RandomBytes(big, 21))
	env.Start()

	env.Emu.SetMediaRate(6 * mib)
	env.Emu.SetMediaDelay(500 * time.Millisecond)
	fh := openFile(t, env, "f.bin")
	mustRead(t, env, fh, 0, mib)
	if c := env.FS.WindowCap(); c != 4*mib {
		t.Fatalf("at 6 MiB/s and a latency of 500 ms the cap is %d, want 4 MiB", c)
	}
}
