package config

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

const MinCacheSize = 1 << 30

const (
	DefaultTransferTime  = 10 * time.Second
	DefaultPrefetchShare = 1.0 / 8
)

type Config struct {
	MountIn      string
	MountPoint   string
	CacheDir     string
	CacheSize    int64
	ClientIDFile string
	TokenFile    string
	UID          uint32
	GID          uint32

	TransferTime time.Duration

	PrefetchShare float64

	MaxDownloadSpeed int64
	MaxUploadSpeed   int64

	FileMode, DirMode uint32
}

type Error struct {
	Key    string
	Reason string
}

func (e *Error) Error() string { return fmt.Sprintf("config: %s: %s", e.Key, e.Reason) }

type file struct {
	MountIn      string `toml:"mount_in"`
	CacheDir     string `toml:"cache_dir"`
	CacheSize    string `toml:"cache_size"`
	ClientIDFile string `toml:"client_id_file"`
	TokenFile    string `toml:"token_file"`
	UID          int64  `toml:"uid"`
	GID          int64  `toml:"gid"`

	TransferTime     string  `toml:"transfer_time"`
	PrefetchShare    float64 `toml:"prefetch_share"`
	MaxDownloadSpeed string  `toml:"max_download_speed"`
	MaxUploadSpeed   string  `toml:"max_upload_speed"`
	FileMode         string  `toml:"file_mode"`
	DirMode          string  `toml:"dir_mode"`
}

const (
	DefaultFileMode = 0o600
	DefaultDirMode  = 0o700
)

func Load(path string) (*Config, error) {

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return Parse(string(data))
}

func Parse(data string) (*Config, error) {
	var f file
	md, err := toml.Decode(data, &f)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if md.IsDefined("mount_point") {
		return nil, &Error{Key: "mount_point", Reason: "is replaced by mount_in, the folder the daemon keeps its " +
			"mount folder in, " + MountPrefix + "<the email of the account>"}
	}
	if keys := md.Undecoded(); len(keys) > 0 {
		return nil, &Error{Key: keys[0].String(), Reason: "unknown key"}
	}
	if err := checkRequired(&md); err != nil {
		return nil, err
	}

	c := &Config{
		TransferTime: DefaultTransferTime, PrefetchShare: DefaultPrefetchShare,
		FileMode: DefaultFileMode, DirMode: DefaultDirMode,
	}
	for _, read := range []func(*toml.MetaData, *file, *Config) error{
		readTransferTime, readPrefetchShare, readSpeeds, readModes, readPaths, readCacheSize, readOwner,
	} {
		if err := read(&md, &f, c); err != nil {
			return nil, err
		}
	}
	return c, nil
}

const MountPrefix = "FastGD - "

func MountName(email string) string {

	for _, d := range []string{"@gmail.com", "@googlemail.com"} {
		if n := len(email) - len(d); n >= 0 && strings.EqualFold(email[n:], d) {
			return email[:n]
		}
	}
	return email
}

func MountFolder(mountIn, email string) string {
	return filepath.Join(mountIn, MountPrefix+MountName(email))
}

func Inside(path, dir string) bool {
	return path == dir || dir == "/" || strings.HasPrefix(path, dir+"/")
}

const (
	kib = 1 << 10
	mib = 1 << 20
	gib = 1 << 30
	tib = 1 << 40
)

func unitMultiple(letter byte) int64 {
	switch letter {
	case 'K', 'k':
		return kib
	case 'M', 'm':
		return mib
	case 'G', 'g':
		return gib
	case 'T', 't':
		return tib
	}
	return 1
}

func ParseSize(s string) (int64, error) {
	t := strings.TrimSpace(s)
	t = strings.TrimSuffix(strings.TrimSuffix(t, "B"), "i")
	if t == "" {
		return 0, fmt.Errorf("%q is not a size", s)
	}
	mult := unitMultiple(t[len(t)-1])
	if mult > 1 {
		t = t[:len(t)-1]
	}
	n, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
	if err != nil || n <= 0 || math.IsInf(n, 0) {
		return 0, fmt.Errorf("%q is not a size, expected something like 50G", s)
	}
	v := n * float64(mult)

	if v >= math.MaxInt64 {
		return 0, fmt.Errorf("%q is too large", s)
	}
	return int64(v), nil
}
