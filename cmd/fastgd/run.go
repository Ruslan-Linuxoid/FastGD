package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/coreos/go-systemd/v22/daemon"
	"github.com/hanwen/go-fuse/v2/fuse"

	"github.com/Ruslan-Linuxoid/FastGD/internal/auth"
	"github.com/Ruslan-Linuxoid/FastGD/internal/binds"
	"github.com/Ruslan-Linuxoid/FastGD/internal/config"
	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/fusefs"
	"github.com/Ruslan-Linuxoid/FastGD/internal/mounts"
)

const defaultConfig = "/etc/fastgd/config.toml"

const driveURLEnv = "FASTGD_DRIVE_URL"

const cacheLimitEnv = "FASTGD_CACHE_LIMIT"

const dieAfterEnv = "FASTGD_DIE_AFTER"

const stopGraceEnv = "FASTGD_STOP_GRACE"

func runDaemon(args []string) error {
	cfg, priv, err := runConfig(args)
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	cl, err := driveClient(ctx, cfg, log)
	if err != nil {
		return err
	}
	if cfg.MountPoint, err = mountFolder(ctx, cfg, askEmail(cl), log); err != nil {
		return err
	}
	if err := prepareMountPoint(cfg.MountPoint, log); err != nil {
		return err
	}
	env, err := testOverrides(cfg, log)
	if err != nil {
		return err
	}
	fsys, err := core.Open(ctx, core.Options{
		CacheDir: cfg.CacheDir, Drive: cl, UID: cfg.UID, GID: cfg.GID, Log: log,
		FileMode: cfg.FileMode, DirMode: cfg.DirMode,
		CacheSize: env.size, TransferTime: cfg.TransferTime, PrefetchShare: cfg.PrefetchShare,
		MinCacheLimit: env.least, ShelveFault: env.shelveFault,
	})
	if err != nil && ctx.Err() != nil {

		log.Info("stopped before the mount was up", "err", err)
		return nil
	}
	if err != nil {
		return err
	}
	bm := openBinds(cfg, priv, log)
	srv, err := fusefs.Mount(cfg.MountPoint, fsys)
	if err != nil {
		return errors.Join(err, fsys.Close())
	}
	log.Info("mounted", "at", cfg.MountPoint)
	m := &mounted{cfg: cfg, log: log, fsys: fsys, srv: srv, grace: env.grace, binds: bm}
	m.unbind = watchBinds(bm, fsys, log)
	return serveMounted(ctx.Done(), m)
}

type mounted struct {
	cfg  *config.Config
	log  *slog.Logger
	fsys *core.FS
	srv  *fuse.Server

	grace time.Duration
	binds *binds.Manager

	unbind func()

	served chan struct{}

	stopControl func()

	closeCore func() error
}

func serveMounted(stopped <-chan struct{}, m *mounted) error {
	m.fsys.SetNotifier(fusefs.Notifier(m.srv))

	background, stopBackground := context.WithCancel(context.Background())
	var ran sync.WaitGroup
	ran.Go(func() { m.fsys.Run(background) })

	stopControl, err := serveControl(controlSocket(m.cfg.CacheDir), m.fsys, m.binds, m.log)
	if err != nil {
		m.log.Warn("the control socket is not open: the sync command will find no daemon", "err", err)
		stopControl = func() {}
	}
	defer stopControl()
	m.stopControl = stopControl
	m.closeCore = func() error {
		stopControl()
		stopBackground()
		ran.Wait()
		return m.fsys.Close()
	}

	notifySystemd(m.log, daemon.SdNotifyReady)

	m.served = make(chan struct{})
	go func() {
		m.srv.Wait()
		close(m.served)
	}()
	if ended, err := m.endedEarly(stopped); ended {
		return err
	}
	return m.shutDown()
}

func notifySystemd(log *slog.Logger, state string) {
	if _, err := daemon.SdNotify(false, state); err != nil {
		log.Warn("notify systemd", "err", err)
	}
}

func (m *mounted) endedEarly(stopped <-chan struct{}) (bool, error) {
	select {
	case <-m.served:

		m.unbind()
		return true, errors.Join(errors.New("the mount was taken down from outside"), m.closeCore())
	case <-m.fsys.Distrusted():

		m.unbind()
		unmount(m.srv, m.cfg.MountPoint, m.log)
		return true, errors.Join(core.ErrUntrusted, m.closeCore())
	case <-stopped:
		return false, nil
	}
}

func (m *mounted) shutDown() error {
	m.log.Info("unmounting")
	notifySystemd(m.log, daemon.SdNotifyStopping)
	m.unbind()
	unmount(m.srv, m.cfg.MountPoint, m.log)

	m.stopControl()
	if !m.fsys.Drain(m.grace) {
		m.log.Warn("operations were still under way when the time to stop ran out: they go on at the next start",
			"waited", m.grace)
	}
	select {
	case <-m.served:
		m.log.Info("unmounted")
		return m.closeCore()
	case <-time.After(time.Second):

		m.log.Warn("files still open on the detached mount, exiting")
		return nil
	}
}

func driveClient(ctx context.Context, cfg *config.Config, log *slog.Logger) (*drive.Client, error) {
	speed := drive.Speed{Download: cfg.MaxDownloadSpeed, Upload: cfg.MaxUploadSpeed}
	if u := os.Getenv(driveURLEnv); u != "" {
		return drive.New(ctx, drive.Options{BaseURL: u, HTTPClient: &http.Client{Transport: drive.Transport()}, Speed: speed})
	}
	hc, err := auth.HTTPClient(ctx, cfg.ClientIDFile, cfg.TokenFile, drive.Transport(), log)
	if err != nil {
		return nil, err
	}
	return drive.New(ctx, drive.Options{HTTPClient: hc, Speed: speed})
}

func prepareMountPoint(dir string, log *slog.Logger) error {
	types, err := mounts.At(dir)
	if err != nil {
		return err
	}
	if len(types) == 0 {
		return makeMountFolder(dir)
	}
	_, err = os.ReadDir(dir)
	switch {
	case err == nil:
		return fmt.Errorf("%s is already mounted (%s)", dir, types[len(types)-1])
	case errors.Is(err, syscall.ENOTCONN):
		log.Warn("clearing the dead mount of a previous run", "at", dir)
		return detach(dir)
	}
	return err
}

func unmount(srv *fuse.Server, dir string, log *slog.Logger) {
	err := srv.Unmount()
	if err == nil {
		return
	}
	log.Warn("mount is busy, detaching it", "err", err)
	if err := detach(dir); err != nil {
		log.Error("detach mount", "at", dir, "err", err)
	}
}

func detach(dir string) error {
	err := syscall.Unmount(dir, syscall.MNT_DETACH)
	if err == nil {
		return nil
	}

	out, ferr := exec.CommandContext(context.Background(), "fusermount3", "-u", "-z", dir).CombinedOutput()
	if ferr != nil {
		return fmt.Errorf("umount: %w; fusermount3: %w: %s", err, ferr, bytes.TrimSpace(out))
	}
	return nil
}
