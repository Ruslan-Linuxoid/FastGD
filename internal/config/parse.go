package config

import (
	"fmt"
	"math"
	"path/filepath"
	"strconv"
	"time"

	"github.com/BurntSushi/toml"
)

func checkRequired(md *toml.MetaData) error {

	required := []string{"mount_in", "cache_dir", "cache_size", "token_file", "uid", "gid"}
	for _, k := range required {
		if !md.IsDefined(k) {
			return &Error{Key: k, Reason: "missing"}
		}
	}
	return nil
}

func readTransferTime(md *toml.MetaData, f *file, c *Config) error {
	if !md.IsDefined("transfer_time") {
		return nil
	}
	d, err := time.ParseDuration(f.TransferTime)
	if err != nil || d <= 0 {
		return &Error{Key: "transfer_time", Reason: fmt.Sprintf("%q is not a duration like 10s", f.TransferTime)}
	}
	c.TransferTime = d
	return nil
}

func readPrefetchShare(md *toml.MetaData, f *file, c *Config) error {
	if !md.IsDefined("prefetch_share") {
		return nil
	}
	if f.PrefetchShare <= 0 || f.PrefetchShare > 1 {
		return &Error{Key: "prefetch_share", Reason: fmt.Sprintf("%v is not a share of the cache between 0 and 1",
			f.PrefetchShare)}
	}
	c.PrefetchShare = f.PrefetchShare
	return nil
}

func readSpeeds(_ *toml.MetaData, f *file, c *Config) error {
	for _, s := range []struct {
		key, val string
		dst      *int64
	}{
		{"max_download_speed", f.MaxDownloadSpeed, &c.MaxDownloadSpeed},
		{"max_upload_speed", f.MaxUploadSpeed, &c.MaxUploadSpeed},
	} {

		if s.val == "" {
			continue
		}
		n, err := ParseSize(s.val)
		if err != nil || n < 1 {
			return &Error{Key: s.key,
				Reason: fmt.Sprintf("%q is not a speed in bytes per second like 10M; leave it empty for no cap", s.val)}
		}
		*s.dst = n
	}
	return nil
}

func readModes(md *toml.MetaData, f *file, c *Config) error {
	for _, m := range []struct {
		key, val string
		dst      *uint32
	}{
		{"file_mode", f.FileMode, &c.FileMode},
		{"dir_mode", f.DirMode, &c.DirMode},
	} {
		if !md.IsDefined(m.key) {
			continue
		}
		n, err := strconv.ParseUint(m.val, 8, 32)
		if err != nil || n > 0o777 {
			return &Error{Key: m.key, Reason: fmt.Sprintf("%q is not a mode of permission bits in octal like \"0640\"",
				m.val)}
		}
		*m.dst = uint32(n)
	}
	return nil
}

func readPaths(md *toml.MetaData, f *file, c *Config) error {
	for _, p := range []struct {
		key string
		val string
		dst *string
	}{
		{"mount_in", f.MountIn, &c.MountIn},
		{"cache_dir", f.CacheDir, &c.CacheDir},
		{"client_id_file", f.ClientIDFile, &c.ClientIDFile},
		{"token_file", f.TokenFile, &c.TokenFile},
	} {
		if p.key == "client_id_file" && !md.IsDefined(p.key) {
			continue
		}
		if !filepath.IsAbs(p.val) {
			return &Error{Key: p.key, Reason: fmt.Sprintf("%q is not an absolute path", p.val)}
		}
		*p.dst = filepath.Clean(p.val)
	}
	return nil
}

func readCacheSize(_ *toml.MetaData, f *file, c *Config) error {
	size, err := ParseSize(f.CacheSize)
	if err != nil {
		return &Error{Key: "cache_size", Reason: err.Error()}
	}
	if size < MinCacheSize {
		return &Error{Key: "cache_size",
			Reason: fmt.Sprintf("%q is less than %dG, the smallest cache the daemon is of use with", f.CacheSize,
				MinCacheSize/gib)}
	}
	c.CacheSize = size
	return nil
}

func readOwner(_ *toml.MetaData, f *file, c *Config) error {
	for _, p := range []struct {
		key string
		val int64
		dst *uint32
	}{{"uid", f.UID, &c.UID}, {"gid", f.GID, &c.GID}} {
		if p.val < 0 || p.val > math.MaxUint32 {
			return &Error{Key: p.key, Reason: fmt.Sprintf("%d is out of range", p.val)}
		}
		*p.dst = uint32(p.val)
	}
	return nil
}
