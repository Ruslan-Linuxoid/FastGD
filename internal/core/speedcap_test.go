package core_test

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func capped(total, bytesPerSecond int64) time.Duration {
	return time.Duration(float64(total-64<<10) / float64(bytesPerSecond) * float64(time.Second))
}

func requireTook(t *testing.T, what string, took, want time.Duration) {
	t.Helper()
	if d := took - want; d < -20*time.Millisecond || d > 20*time.Millisecond {
		t.Fatalf("%s took %v, want %v", what, took, want)
	}
}

func TestDownloadsTogetherGoAtTheCap(t *testing.T) {
	const speed = 4 << 20
	inBubble(t, drive.Options{Speed: drive.Speed{Download: speed}}, func(t *testing.T, emu *emulator.Emulator,
		cl *drive.Client) {
		ctx := context.Background()
		sink := func([]byte) error { return nil }
		ids := make([]string, 0, 4)
		for i := range 4 {
			ids = append(ids, emu.WriteFile(emu.Root(), string(rune('a'+i)), testenv.RandomBytes(mib, uint64(300+i))))
		}

		start := time.Now()
		var wg sync.WaitGroup
		for _, id := range ids {
			wg.Go(func() {
				if err := cl.Stream(ctx, id, 0, mib, sink); err != nil {
					t.Error(err)
				}
			})
		}
		wg.Wait()
		requireTook(t, "4 MiB of downloads at 4 MiB/s", time.Since(start), capped(4*mib, speed))
	})
}

func TestUploadsTogetherGoAtTheCap(t *testing.T) {
	const speed = 4 << 20
	inBubble(t, drive.Options{Speed: drive.Speed{Upload: speed}}, func(t *testing.T, emu *emulator.Emulator,
		cl *drive.Client) {
		ctx := context.Background()
		ids, err := cl.GenerateIDs(ctx, 4)
		if err != nil {
			t.Fatal(err)
		}
		content := testenv.RandomBytes(mib, 304)
		body := func() io.Reader { return bytes.NewReader(content) }

		if _, err := cl.CreateFile(ctx, ids[2], "old.bin", emu.Root(), "application/octet-stream", time.Now(), 0,
			func() io.Reader { return bytes.NewReader(nil) }); err != nil {
			t.Fatal(err)
		}
		session, err := cl.StartCreate(ctx, ids[3], "session.bin", emu.Root(), "application/octet-stream", time.Now(), mib)
		if err != nil {
			t.Fatal(err)
		}
		start := time.Now()
		var wg sync.WaitGroup
		for _, send := range []func() error{
			func() error {
				_, err := cl.CreateFile(ctx, ids[0], "a.bin", emu.Root(), "application/octet-stream", time.Now(), mib, body)
				return err
			},
			func() error {
				_, err := cl.CreateFile(ctx, ids[1], "b.bin", emu.Root(), "application/octet-stream", time.Now(), mib, body)
				return err
			},
			func() error {
				_, err := cl.UpdateContent(ctx, ids[2], "application/octet-stream", time.Now(), mib, body)
				return err
			},
			func() error {
				_, err := cl.SendChunk(ctx, session, 0, mib, mib, body())
				return err
			},
		} {
			wg.Go(func() {
				if err := send(); err != nil {
					t.Error(err)
				}
			})
		}
		wg.Wait()
		requireTook(t, "4 MiB of uploads at 4 MiB/s", time.Since(start), capped(4*mib, speed))
		for _, id := range ids {
			if f, ok := emu.Get(id); !ok || !bytes.Equal(f.Content, content) {
				t.Fatalf("%s went up different", id)
			}
		}
	})
}

func TestWithoutACapNothingWaits(t *testing.T) {
	inBubble(t, drive.Options{}, func(t *testing.T, emu *emulator.Emulator, cl *drive.Client) {
		id := emu.WriteFile(emu.Root(), "f.bin", testenv.RandomBytes(4*mib, 305))
		start := time.Now()
		if err := cl.Stream(context.Background(), id, 0, 4*mib, func([]byte) error { return nil }); err != nil {
			t.Fatal(err)
		}
		if took := time.Since(start); took != 0 {
			t.Fatalf("a download without a cap waited %v of the bubble's time", took)
		}
	})
}

func TestTheDownloadCapBoundsTheWindow(t *testing.T) {
	env := testenv.New(t)
	env.SetSpeed(drive.Speed{Download: 256 * kib})
	env.Emu.WriteFile(env.Emu.Root(), "f.bin", testenv.RandomBytes(160*kib, 306))
	env.Start()

	readAll(t, env, "f.bin")
	if c := env.FS.WindowCap(); c != 2*mib {
		t.Fatalf("with downloads held to 256 KiB/s the window cap is %d, want 2 MiB", c)
	}
}

func TestTheUploadCapBoundsTheChunk(t *testing.T) {
	env := testenv.New(t)
	env.TransferTime = time.Second
	env.SetSpeed(drive.Speed{Upload: 3 * mib / 2})
	smallSessions(env)
	env.Start()

	env.FS.SeedUploadSpeed(64 * mib)
	writeFile(t, env, "", "f.bin", testenv.RandomBytes(sessionFrom+8*mib, 307))
	for _, n := range chunksUpTo(t, env, mib) {
		if n != mib {
			t.Fatalf("a chunk of %d bytes with uploads held to 1.5 MiB/s, want 1 MiB", n)
		}
	}
}

func TestALowCapIsNotPassedAtTheStart(t *testing.T) {
	const speed = 10 << 10
	inBubble(t, drive.Options{Speed: drive.Speed{Download: speed}}, func(t *testing.T, emu *emulator.Emulator,
		cl *drive.Client) {
		id := emu.WriteFile(emu.Root(), "f.bin", testenv.RandomBytes(100*kib, 308))
		start := time.Now()
		if err := cl.Stream(context.Background(), id, 0, 100*kib, func([]byte) error { return nil }); err != nil {
			t.Fatal(err)
		}

		requireTook(t, "100 KiB at 10 KiB/s", time.Since(start), 9*time.Second)
	})
}
