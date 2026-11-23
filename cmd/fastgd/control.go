package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

func controlSocket(cacheDir string) string { return filepath.Join(cacheDir, "control.sock") }

type syncer interface {
	SyncNow(ctx context.Context) (left int, err error)
}

func serveControl(path string, daemon syncer, binder binder, log *slog.Logger) (stop func(), err error) {

	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		ln.Close()
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	var requests sync.WaitGroup
	requests.Go(func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			requests.Go(func() {
				defer conn.Close()
				answer(ctx, conn, daemon, binder, log)
			})
		}
	})
	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			ln.Close()
			requests.Wait()
		})
	}, nil
}

const requestWait = 5 * time.Second

func answer(ctx context.Context, conn net.Conn, daemon syncer, binder binder, log *slog.Logger) {
	_ = conn.SetReadDeadline(time.Now().Add(requestWait))
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return
	}
	req := strings.TrimSpace(line)
	if body, ok := strings.CutPrefix(req, mountRequest); ok {
		fmt.Fprintln(conn, answerMount(binder, body, log))
		return
	}
	switch req {
	case "sync":
		log.Info("a sync was asked for")
		left, err := daemon.SyncNow(ctx)
		fmt.Fprintln(conn, syncAnswer(left, err))
	default:
		fmt.Fprintf(conn, "error unknown request %q\n", req)
	}
}

func syncAnswer(left int, err error) string {
	if err != nil {
		return fmt.Sprintf("error %v", err)
	}
	return fmt.Sprintf("left %d", left)
}

func parseSyncAnswer(line string) (left int, err error) {
	count, isLeft := strings.CutPrefix(line, "left ")
	left, err = strconv.Atoi(count)
	if !isLeft || err != nil {
		return 0, fmt.Errorf("the daemon answered: %s", strings.TrimPrefix(line, "error "))
	}
	return left, nil
}

func ask(path, req string) (string, error) {
	conn, err := (&net.Dialer{}).DialContext(context.Background(), "unix", path)
	if err != nil {
		return "", fmt.Errorf("the daemon is not running: nobody answers at %s (%w)", path, errors.Unwrap(err))
	}
	defer conn.Close()
	if _, err := fmt.Fprintln(conn, req); err != nil {
		return "", err
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("the daemon gave no answer: %w", err)
	}
	return strings.TrimSpace(line), nil
}
