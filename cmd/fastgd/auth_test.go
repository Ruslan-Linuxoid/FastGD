package main

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/auth"
	"github.com/Ruslan-Linuxoid/FastGD/internal/config"
)

func TestADaemonWithoutATokenSaysToRunAuth(t *testing.T) {
	t.Setenv(driveURLEnv, "")
	dir := t.TempDir()
	cfg := &config.Config{ClientIDFile: filepath.Join(dir, "client_id.json"), TokenFile: filepath.Join(dir, "token.json")}
	client := `{"installed":{"client_id":"id","auth_uri":"http://127.0.0.1:1/a","token_uri":"http://127.0.0.1:1/t"}}`
	if err := os.WriteFile(cfg.ClientIDFile, []byte(client), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfg.TokenFile, []byte(`{"access_token":"a"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := driveClient(t.Context(), cfg, slog.New(slog.DiscardHandler)); !errors.Is(err, auth.ErrNoToken) {
		t.Fatalf("the start without a refresh token ends with %v", err)
	}
}

func TestABuildWithoutTheKeyOfTheAppNamesClientIDFile(t *testing.T) {
	t.Setenv(driveURLEnv, "")
	dir := t.TempDir()
	if err := authCmd([]string{"-token", filepath.Join(dir, "token.json")}); !errors.Is(err, errNoKeyForAuth) ||
		!strings.Contains(err.Error(), "-client-id") || strings.Contains(err.Error(), "config") {
		t.Errorf("auth without a key of the app gives %v, want it to name -client-id and no config", err)
	}
	cfg := &config.Config{TokenFile: filepath.Join(dir, "token.json")}
	if _, err := driveClient(t.Context(), cfg, slog.New(slog.DiscardHandler)); !errors.Is(err, auth.ErrNoAppClient) {
		t.Errorf("run without a key of the app gives %v", err)
	}
}

func TestAuthTakesTheClientOfItsFlag(t *testing.T) {
	dir := t.TempDir()
	web := filepath.Join(dir, "web.json")
	if err := os.WriteFile(web, []byte(`{"web":{"client_id":"w"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	err := authCmd([]string{"-client-id", web, "-token", filepath.Join(dir, "token.json")})
	if err == nil || !strings.Contains(err.Error(), web) || !strings.Contains(err.Error(), "Web application") {
		t.Errorf("auth with -client-id of a Web client gives %v, want the refusal of that file", err)
	}
}
