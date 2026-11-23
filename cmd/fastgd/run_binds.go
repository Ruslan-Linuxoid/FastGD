package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/user"
	"strconv"
	"sync"
	"syscall"

	"github.com/Ruslan-Linuxoid/FastGD/internal/binds"
	"github.com/Ruslan-Linuxoid/FastGD/internal/config"
	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
)

const helperCommand = "bind-helper"

func helperCmd(args []string) error {
	fl := flag.NewFlagSet(helperCommand, flag.ContinueOnError)
	mountIn := fl.String("mount-in", "", "the folder the mount folders of the daemon are in")
	if err := fl.Parse(args); err != nil {
		return err
	}
	return binds.RunHelper(*mountIn, os.NewFile(binds.HelperFD, "daemon"))
}

func runConfig(args []string) (*config.Config, privileges, error) {
	var runAs string
	cfg, _, err := loadConfigArgs("run", args, func(fl *flag.FlagSet) {
		fl.StringVar(&runAs, "user", "", "the user a start as root makes the daemon, with root left to the helper "+
			"that binds the folders of the list")
	})
	if err != nil {
		return nil, privileges{}, err
	}
	priv, err := takePrivileges(cfg, runAs)
	return cfg, priv, err
}

type privileges struct {
	root bool

	helper *binds.Helper
}

func takePrivileges(cfg *config.Config, runAs string) (privileges, error) {
	if os.Geteuid() != 0 {
		if runAs != "" {
			return privileges{}, fmt.Errorf("--user %s needs a start as root", runAs)
		}
		return privileges{}, nil
	}
	p := privileges{root: true}

	if rows, err := binds.Load(cfg.CacheDir); err == nil && len(rows) > 0 {
		self, err := os.Executable()
		if err != nil {
			return privileges{}, err
		}
		if p.helper, err = binds.StartHelper(self, helperCommand, "--mount-in", cfg.MountIn); err != nil {
			return privileges{}, err
		}
	}
	if runAs != "" {
		if err := becomeUser(runAs); err != nil {
			return privileges{}, err
		}
	}
	return p, nil
}

func becomeUser(name string) error {
	u, err := user.Lookup(name)
	if err != nil {
		return err
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return err
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return err
	}
	groups, err := userGroups(u)
	if err != nil {
		return err
	}

	if err := errors.Join(syscall.Setgroups(groups), syscall.Setgid(gid), syscall.Setuid(uid)); err != nil {
		return fmt.Errorf("become %s: %w", name, err)
	}
	if os.Geteuid() == 0 || os.Getegid() == 0 && gid != 0 {
		return fmt.Errorf("become %s: root is still held", name)
	}
	return nil
}

func userGroups(u *user.User) ([]int, error) {
	ids, err := u.GroupIds()
	if err != nil {
		return nil, err
	}
	groups := make([]int, 0, len(ids))
	for _, id := range ids {
		g, err := strconv.Atoi(id)
		if err != nil {
			return nil, err
		}
		groups = append(groups, g)
	}
	return groups, nil
}

func openBinds(cfg *config.Config, p privileges, log *slog.Logger) *binds.Manager {
	o := binds.Options{Dir: cfg.CacheDir, MountPoint: cfg.MountPoint, Root: p.root, Log: log}
	if p.helper != nil {
		o.Helper = p.helper
	}
	return binds.New(o)
}

func watchBinds(m *binds.Manager, fsys *core.FS, log *slog.Logger) (stop func()) {
	if err := m.Start(fsys); err != nil {
		log.Error("bind the folders of the list", "err", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var watched sync.WaitGroup
	watched.Go(func() { m.Watch(ctx) })
	var once sync.Once
	return func() {
		once.Do(func() {
			cancel()
			watched.Wait()
			m.Stop()
		})
	}
}
