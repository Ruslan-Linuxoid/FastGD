package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"strings"

	"github.com/Ruslan-Linuxoid/FastGD/internal/auth"
	"github.com/Ruslan-Linuxoid/FastGD/internal/config"
	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
)

func mountFolder(ctx context.Context, cfg *config.Config, ask func(context.Context) (string, error),
	log *slog.Logger,
) (string, error) {
	email, err := auth.ReadEmail(cfg.TokenFile)
	if err != nil {
		return "", err
	}
	if email == "" {
		if email, err = ask(ctx); err != nil {
			return "", fmt.Errorf("the email of the account names the mount folder, and neither %s beside the token "+
				"holds it nor Drive tells it: %w; the first start needs the network", auth.EmailFile(cfg.TokenFile), err)
		}
		if err := checkEmail(email); err != nil {
			return "", err
		}
		if err := auth.SaveEmail(cfg.TokenFile, email); err != nil {
			log.Warn("the email of the account is not kept: a start without the network will not find it", "err", err)
		}
	}
	if err := checkEmail(email); err != nil {
		return "", err
	}
	dir := config.MountFolder(cfg.MountIn, email)
	if config.Inside(cfg.CacheDir, dir) {
		return "", &config.Error{Key: "cache_dir", Reason: "must not be inside the mount folder " + dir}
	}
	return dir, nil
}

func checkEmail(email string) error {
	if name := config.MountName(email); name == "" || strings.ContainsAny(name, "/\x00") {
		return fmt.Errorf("%q names no mount folder: it is no email of an account", email)
	}
	return nil
}

var errNoEmail = errors.New("no email of the account in the answer of Drive")

func askEmail(cl *drive.Client) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		a, err := cl.Account(ctx)
		if err == nil && a.Email == "" {
			err = errNoEmail
		}
		return a.Email, err
	}
}

func makeMountFolder(dir string) error {

	err := os.MkdirAll(dir, 0o755)
	if errors.Is(err, fs.ErrPermission) {
		return fmt.Errorf("cannot make the mount folder: %w; make %s and give it to the user the daemon runs as", err,
			dir)
	}
	return err
}
