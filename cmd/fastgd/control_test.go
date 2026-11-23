package main

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeSync struct {
	left int
	err  error
	hang bool

	entered chan struct{}
	ended   *atomic.Bool
}

func (s fakeSync) SyncNow(ctx context.Context) (int, error) {
	if s.hang {
		if s.entered != nil {
			close(s.entered)
		}
		<-ctx.Done()
		if s.ended != nil {
			time.Sleep(50 * time.Millisecond)
			s.ended.Store(true)
		}
		return 0, ctx.Err()
	}
	return s.left, s.err
}

func TestControlSocketAnswersASync(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	for name, c := range map[string]struct {
		daemon fakeSync
		req    string
		want   string
	}{
		"all sent":        {fakeSync{}, "sync", "left 0"},
		"some left":       {fakeSync{left: 3}, "sync", "left 3"},
		"failed":          {fakeSync{err: errors.New("the database is closed")}, "sync", "error the database is closed"},
		"unknown request": {fakeSync{}, "reboot", `error unknown request "reboot"`},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "control.sock")
			stop, err := serveControl(path, c.daemon, nil, log)
			if err != nil {
				t.Fatal(err)
			}
			got, err := ask(path, c.req)
			if err != nil || got != c.want {
				t.Fatalf("the daemon answered %q: %v, want %q", got, err, c.want)
			}
			stop()
			if _, err := ask(path, c.req); err == nil || !strings.Contains(err.Error(), "the daemon is not running") {
				t.Fatalf("after the stop a request got %v, want it told that no daemon runs", err)
			}
		})
	}
}

func TestControlSocketStopsWithARequestUnderWay(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	path := filepath.Join(t.TempDir(), "control.sock")
	entered, ended := make(chan struct{}), &atomic.Bool{}
	stop, err := serveControl(path, fakeSync{hang: true, entered: entered, ended: ended}, nil, log)
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan string, 1)
	go func() {
		line, _ := ask(path, "sync")
		got <- line
	}()

	<-entered
	stop()
	if !ended.Load() {
		t.Fatal("the stop returned before the sync under way did")
	}
	if line := <-got; !strings.HasPrefix(line, "error ") {
		t.Fatalf("a sync that the stop called off answered %q, want an error", line)
	}
	again, err := serveControl(path, fakeSync{}, nil, log)
	if err != nil {
		t.Fatalf("a second run cannot listen where the first did: %v", err)
	}
	again()
}
