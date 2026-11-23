package core_test

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
)

func retryCalls(ctx context.Context, emu *emulator.Emulator, cl *drive.Client, token string, id string,
	ids []string) map[string]func() error {
	root := emu.Root()
	return map[string]func() error{
		"list": func() error { return cl.ListAll(ctx, func([]drive.File) error { return nil }) },
		"changes": func() error {
			_, _, err := cl.Changes(ctx, token)
			return err
		},
		"download": func() error {
			return cl.Stream(ctx, id, 0, 7, func([]byte) error { return nil })
		},
		"create folder": func() error {
			_, err := cl.CreateFolder(ctx, ids[0], "d", root, time.Now())
			return err
		},
		"create file": func() error {
			_, err := cl.CreateFile(ctx, ids[1], "g.bin", root, "application/octet-stream", time.Now(), 3,
				func() io.Reader { return strings.NewReader("new") })
			return err
		},
		"update": func() error {
			_, err := cl.UpdateContent(ctx, id, "application/octet-stream", time.Now(), 7,
				func() io.Reader { return strings.NewReader("changed") })
			return err
		},
		"trash": func() error {
			_, err := cl.UpdateMeta(ctx, id, drive.MetaUpdate{Trashed: true})
			return err
		},
	}
}

func TestEveryCallGoesAgainAfterErrorsThatPass(t *testing.T) {

	inBubble(t, drive.Options{Limits: drive.Limits{Units: 325_000, UnitsWindow: time.Minute, RetryFor: 5 * time.Second,
		PerSecond: 1e6}}, func(t *testing.T, emu *emulator.Emulator, cl *drive.Client) {
		ctx := context.Background()
		root := emu.Root()
		id := emu.WriteFile(root, "f.bin", []byte("content"))
		token, err := cl.StartPageToken(ctx)
		if err != nil {
			t.Fatal(err)
		}
		ids, err := cl.GenerateIDs(ctx, 3)
		if err != nil {
			t.Fatal(err)
		}
		calls := retryCalls(ctx, emu, cl, token, id, ids)
		failures := []struct {
			code   int
			reason string
		}{{500, "backendError"}, {503, "backendError"}, {429, "rateLimitExceeded"}, {403, "userRateLimitExceeded"}, {403,
			"rateLimitExceeded"}}
		for _, name := range []string{"list", "changes", "download", "create folder", "create file", "update", "trash"} {
			for _, f := range failures {
				emu.FailRequests(1, f.code, f.reason, 0)

				if err := calls[name](); err != nil && !errors.Is(err, drive.ErrExists) {
					t.Fatalf("%s after %d %s: %v", name, f.code, f.reason, err)
				}
			}
		}

		emu.SetOffline(true)
		go func() {
			time.Sleep(time.Second)
			emu.SetOffline(false)
		}()
		if _, err := cl.Get(ctx, root); err != nil {
			t.Fatalf("a call made while the network came back: %v", err)
		}
	})
}

func TestBackgroundCallsGoAgainFromAFifthOfASecondUpToTwoMinutes(t *testing.T) {
	inBubble(t, drive.Options{Limits: drive.DefaultLimits()}, func(t *testing.T, emu *emulator.Emulator,
		cl *drive.Client) {
		emu.FailRequests(1000, 503, "backendError", 0)
		start := time.Now()
		if _, err := cl.Get(context.Background(), emu.Root()); err == nil || time.Since(start) > 15*time.Second {
			t.Fatalf("a call a program waits for ended with %v after %v, want an error within 15 s", err, time.Since(start))
		}
		requireBackgroundRidesOutFailures(t, emu, cl)
		requireBackgroundGivesUp(t, emu, cl)
		requireBackgroundKeepsTheWaitAsked(t, emu, cl)
	})
}

func TestBackgroundCallsDoNotWaitOutALostNetwork(t *testing.T) {
	inBubble(t, drive.Options{Limits: drive.DefaultLimits()}, func(t *testing.T, emu *emulator.Emulator,
		cl *drive.Client) {
		emu.SetOffline(true)
		start := time.Now()
		if _, err := cl.Get(drive.InBackground(context.Background()), emu.Root()); err == nil ||
			time.Since(start) > 30*time.Second {
			t.Fatalf("without the network a background call ended with %v after %v", err, time.Since(start))
		}
	})
}

func requireBackgroundRidesOutFailures(t *testing.T, emu *emulator.Emulator, cl *drive.Client) {
	t.Helper()

	emu.FailRequests(8, 503, "backendError", 0)
	emu.ResetRequests()
	start := time.Now()
	if _, err := cl.Get(drive.InBackground(context.Background()), emu.Root()); err != nil {
		t.Fatalf("the background call gave up after %v: %v", time.Since(start), err)
	}
	if took := time.Since(start); took < 25*time.Second || took > 52*time.Second {
		t.Fatalf("nine tries of the background call took %v, want 25 to 51 s", took)
	}
}

func requireBackgroundGivesUp(t *testing.T, emu *emulator.Emulator, cl *drive.Client) {
	t.Helper()

	emu.FailRequests(1000, 503, "backendError", 0)
	emu.ResetRequests()
	start := time.Now()
	if _, err := cl.Get(drive.InBackground(context.Background()), emu.Root()); err == nil {
		t.Fatal("the background call of a Drive that stays broken ended well")
	}
	if n, took := len(emu.Requests()), time.Since(start); n != 12 || took < 160*time.Second || took > 330*time.Second {
		t.Fatalf("the background call gave up after %d tries and %v, want 12 tries and 2.7 to 5.5 minutes", n, took)
	}
	emu.FailRequests(0, 0, "", 0)
}

func requireBackgroundKeepsTheWaitAsked(t *testing.T, emu *emulator.Emulator, cl *drive.Client) {
	t.Helper()

	emu.FailRequests(1, 429, "rateLimitExceeded", time.Hour)
	start := time.Now()
	if _, err := cl.Get(drive.InBackground(context.Background()), emu.Root()); drive.WaitAsked(err) != time.Hour ||
		time.Since(start) > time.Second {
		t.Fatalf("a background call Drive asked to wait an hour for ended with %v after %v", err, time.Since(start))
	}
	emu.FailRequests(1, 429, "rateLimitExceeded", time.Minute)
	start = time.Now()
	if _, err := cl.Get(drive.InBackground(context.Background()), emu.Root()); err != nil ||
		time.Since(start) < time.Minute {
		t.Fatalf("a background call Drive asked to wait a minute for ended with %v after %v", err, time.Since(start))
	}
}
