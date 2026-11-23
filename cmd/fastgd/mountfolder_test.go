package main

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/auth"
	"github.com/Ruslan-Linuxoid/FastGD/internal/config"
)

func mountConfig(dir string) *config.Config {
	return &config.Config{MountIn: filepath.Join(dir, "in"), CacheDir: filepath.Join(dir, "cache"),
		TokenFile: filepath.Join(dir, "token.json")}
}

func noDrive(context.Context) (string, error) { return "", errors.New("no network") }

func driveTells(context.Context) (string, error) { return "drive@example.com", nil }

func discard() *slog.Logger { return slog.New(slog.DiscardHandler) }

func TestTheMountFolderIsNamedByTheEmail(t *testing.T) {
	dir := t.TempDir()
	cfg := mountConfig(dir)
	got, err := mountFolder(t.Context(), cfg, driveTells, discard())
	if want := filepath.Join(dir, "in", "FastGD - drive@example.com"); err != nil || got != want {
		t.Fatalf("with no email beside the token the mount folder is %q, %v; want %q", got, err, want)
	}

	got, err = mountFolder(t.Context(), cfg, noDrive, discard())
	if want := filepath.Join(dir, "in", "FastGD - drive@example.com"); err != nil || got != want {
		t.Fatalf("without the network the mount folder is %q, %v; want %q", got, err, want)
	}
	if err := auth.SaveEmail(cfg.TokenFile, "kept@example.com"); err != nil {
		t.Fatal(err)
	}
	got, err = mountFolder(t.Context(), cfg, noDrive, discard())
	if want := filepath.Join(dir, "in", "FastGD - kept@example.com"); err != nil || got != want {
		t.Fatalf("with the email beside the token the mount folder is %q, %v; want %q", got, err, want)
	}
}

func TestTheMountFolderNeedsAnEmailAndNoCacheInIt(t *testing.T) {
	dir := t.TempDir()
	cfg := mountConfig(dir)
	if _, err := mountFolder(t.Context(), cfg, noDrive, discard()); err == nil ||
		!strings.Contains(err.Error(), "token.email") || !strings.Contains(err.Error(), "needs the network") {
		t.Fatalf("with no email the start answered %v", err)
	}
	cfg.CacheDir = filepath.Join(dir, "in", "FastGD - you@example.com", "cache")
	if _, err := mountFolder(t.Context(), cfg, func(context.Context) (string, error) {
		return "you@example.com", nil
	}, discard()); err == nil || !strings.Contains(err.Error(), "cache_dir") {
		t.Fatalf("with the cache inside the mount folder the start answered %v", err)
	}
}

func TestAMountFolderThatCannotBeMadeNamesItself(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root makes any folder")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	folder := filepath.Join(dir, "FastGD - you@example.com")
	if err := makeMountFolder(folder); err == nil || !strings.Contains(err.Error(), "make "+folder+" and give it") {
		t.Fatalf("the start answered %v", err)
	}
}

func TestAnEmailWithASlashNamesNoMountFolder(t *testing.T) {
	dir := t.TempDir()
	cfg := mountConfig(dir)
	escape := func(context.Context) (string, error) { return "x/../../etc", nil }
	if _, err := mountFolder(t.Context(), cfg, escape, discard()); err == nil {
		t.Fatal("an email with a slash from Drive named a mount folder")
	}
	if email, err := auth.ReadEmail(cfg.TokenFile); err != nil || email != "" {
		t.Fatalf("the email beside the token reads %q, %v; want none kept", email, err)
	}
	if err := auth.SaveEmail(cfg.TokenFile, "a/b"); err != nil {
		t.Fatal(err)
	}
	if _, err := mountFolder(t.Context(), cfg, driveTells, discard()); err == nil {
		t.Fatal("an email with a slash beside the token named a mount folder")
	}
}

func TestAGmailWithNoNameNamesNoMountFolder(t *testing.T) {
	for _, email := range []string{"@gmail.com", "x/y@gmail.com"} {
		tells := func(context.Context) (string, error) { return email, nil }
		if _, err := mountFolder(t.Context(), mountConfig(t.TempDir()), tells, discard()); err == nil {
			t.Errorf("%q named a mount folder", email)
		}
	}
}
