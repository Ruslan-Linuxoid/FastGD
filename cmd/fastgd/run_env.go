package main

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"syscall"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/config"
	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
)

type overrides struct {
	size, least int64
	grace       time.Duration
	shelveFault func(string) error
}

func testOverrides(cfg *config.Config, log *slog.Logger) (overrides, error) {
	size, least, err := cacheLimit(cfg, log)
	if err != nil {
		return overrides{}, err
	}
	grace, err := stopGrace(log)
	if err != nil {
		return overrides{}, err
	}
	return overrides{size: size, least: least, grace: grace, shelveFault: dieAfter(log)}, nil
}

func cacheLimit(cfg *config.Config, log *slog.Logger) (size, least int64, err error) {
	size, least = cfg.CacheSize, int64(config.MinCacheSize)
	v := os.Getenv(cacheLimitEnv)
	if v == "" {
		return size, least, nil
	}
	if size, err = strconv.ParseInt(v, 10, 64); err != nil || size <= 0 {
		return 0, 0, fmt.Errorf("%s=%q is not a number of bytes above 0", cacheLimitEnv, v)
	}
	log.Warn("the cache limit comes from the environment, not from the config: this is for tests", "env", cacheLimitEnv,
		"bytes", size)
	return size, 0, nil
}

func stopGrace(log *slog.Logger) (time.Duration, error) {
	grace := core.StopGrace
	v := os.Getenv(stopGraceEnv)
	if v == "" {
		return grace, nil
	}
	grace, err := time.ParseDuration(v)
	if err != nil || grace < 0 {
		return 0, fmt.Errorf("%s=%q is not a time to wait", stopGraceEnv, v)
	}
	log.Warn("the time a stop waits for the operations under way comes from the environment: this is for tests", "env",
		stopGraceEnv, "grace", grace)
	return grace, nil
}

func dieAfter(log *slog.Logger) func(string) error {
	step := os.Getenv(dieAfterEnv)
	if step == "" {
		return nil
	}
	log.Warn("the daemon kills itself in a move into Lost and Found: this is for tests", "env", dieAfterEnv, "after",
		step)
	return func(at string) error {
		if at == step {
			_ = syscall.Kill(os.Getpid(), syscall.SIGKILL)

			select {}
		}
		return nil
	}
}
