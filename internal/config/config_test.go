package config

import (
	"errors"
	"strings"
	"testing"
	"time"
)

const valid = `
mount_in = "/mnt"
cache_dir = "/var/cache/fastgd"
cache_size = "50G"
client_id_file = "/etc/fastgd/client_id.json"
token_file = "/var/lib/fastgd/token.json"
uid = 1000
gid = 1001
`

func TestParseValid(t *testing.T) {
	c, err := Parse(valid)
	if err != nil {
		t.Fatal(err)
	}
	want := Config{
		MountIn:       "/mnt",
		CacheDir:      "/var/cache/fastgd",
		CacheSize:     50 << 30,
		ClientIDFile:  "/etc/fastgd/client_id.json",
		TokenFile:     "/var/lib/fastgd/token.json",
		UID:           1000,
		GID:           1001,
		TransferTime:  10 * time.Second,
		PrefetchShare: 0.125,
		FileMode:      DefaultFileMode,
		DirMode:       DefaultDirMode,
	}
	if *c != want {
		t.Fatalf("got %+v, want %+v", *c, want)
	}
}

func TestCacheOfTheMinimumSizeStarts(t *testing.T) {
	c, err := Parse(strings.Replace(valid, `"50G"`, `"1G"`, 1))
	if err != nil {
		t.Fatal(err)
	}
	if c.CacheSize != MinCacheSize {
		t.Fatalf("cache_size 1G parsed as %d", c.CacheSize)
	}
}

func TestSpeedCaps(t *testing.T) {
	c, err := Parse(valid + "max_download_speed = \"10M\"\nmax_upload_speed = \"512K\"\n")
	if err != nil {
		t.Fatal(err)
	}
	if c.MaxDownloadSpeed != 10<<20 || c.MaxUploadSpeed != 512<<10 {
		t.Fatalf("got %d and %d bytes per second", c.MaxDownloadSpeed, c.MaxUploadSpeed)
	}

	c, err = Parse(valid + "max_download_speed = \"\"\n")
	if err != nil || c.MaxDownloadSpeed != 0 || c.MaxUploadSpeed != 0 {
		t.Fatalf("an empty cap gives %d and %d, %v", c.MaxDownloadSpeed, c.MaxUploadSpeed, err)
	}
	requireBadSpeedCapsNamed(t)
}

func requireBadSpeedCapsNamed(t *testing.T) {
	t.Helper()
	for _, c := range [][2]string{{"max_download_speed", "fast"}, {"max_upload_speed", "0"}, {"max_upload_speed", "0.5"}} {
		key, bad := c[0], c[1]
		_, err := Parse(valid + key + " = \"" + bad + "\"\n")
		if err == nil || !strings.Contains(err.Error(), key) || !strings.Contains(err.Error(), bad) {
			t.Errorf("%s = %q gives %v, want an error naming the key and the value", key, bad, err)
		}
	}
}

func TestOptionalKeys(t *testing.T) {
	c, err := Parse(valid + "transfer_time = \"30s\"\nprefetch_share = 0.25\n")
	if err != nil {
		t.Fatal(err)
	}
	if c.TransferTime != 30*time.Second || c.PrefetchShare != 0.25 {
		t.Fatalf("got %v and %v", c.TransferTime, c.PrefetchShare)
	}
	for _, bad := range []string{"transfer_time = \"soon\"\n", "transfer_time = \"-1s\"\n", "prefetch_share = 1.5\n",
		"prefetch_share = 0.0\n"} {
		if _, err := Parse(valid + bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestParseErrorsNameKeyAndReason(t *testing.T) {
	cases := []struct {
		name   string
		edit   func(string) string
		wanted []string
	}{
		{"missing key", func(s string) string { return strings.Replace(s, "uid = 1000\n", "", 1) }, []string{"uid",
			"missing"}},
		{"unknown key", func(s string) string { return s + "cache_sise = \"1G\"\n" }, []string{"cache_sise", "unknown key"}},
		{"bad size", func(s string) string { return strings.Replace(s, `"50G"`, `"50X"`, 1) }, []string{"cache_size", "50X"}},
		{"tiny size", func(s string) string { return strings.Replace(s, `"50G"`, `"1023M"`, 1) }, []string{"cache_size",
			"1G"}},
		{"relative path", func(s string) string { return strings.Replace(s, `"/mnt"`, `"mnt"`, 1) },
			[]string{"mount_in", "absolute"}},
		{"the old key of the mount", func(s string) string {
			return strings.Replace(s, `mount_in = "/mnt"`, `mount_point = "/mnt/gdrive"`, 1)
		}, []string{"mount_point", "replaced by mount_in", "FastGD - <the email of the account>"}},
		{"the old key beside the new", func(s string) string { return s + "mount_point = \"/mnt/gdrive\"\n" },
			[]string{"mount_point", "replaced by mount_in"}},
		{"negative uid", func(s string) string { return strings.Replace(s, "uid = 1000", "uid = -1", 1) }, []string{"uid",
			"out of range"}},
		{"wrong type", func(s string) string { return strings.Replace(s, "gid = 1001", `gid = "x"`, 1) }, []string{"gid"}},
		{"syntax", func(s string) string { return s + "oops\n" }, []string{"config:", "line 9"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse(tc.edit(valid))
			if err == nil {
				t.Fatal("no error")
			}
			for _, w := range tc.wanted {
				if !strings.Contains(err.Error(), w) {
					t.Fatalf("error %q does not mention %q", err, w)
				}
			}
		})
	}
}

func TestParseSize(t *testing.T) {
	cases := map[string]int64{
		"50G":    50 << 30,
		"512M":   512 << 20,
		"1.5T":   3 << 39,
		"10GiB":  10 << 30,
		"64KB":   64 << 10,
		"123456": 123456,
	}
	for in, want := range cases {
		got, err := ParseSize(in)
		if err != nil || got != want {
			t.Errorf("ParseSize(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, bad := range []string{"", "G", "-1G", "abc", "1e400", "8388608T"} {
		if _, err := ParseSize(bad); err == nil {
			t.Errorf("ParseSize(%q) accepted", bad)
		}
	}
}

func TestModesOfTheMount(t *testing.T) {
	t.Parallel()
	c, err := Parse(valid)
	if err != nil {
		t.Fatal(err)
	}
	if c.FileMode != 0o600 || c.DirMode != 0o700 {
		t.Fatalf("without the keys the modes are %o and %o, want 0600 and 0700", c.FileMode, c.DirMode)
	}
	c, err = Parse(valid + "file_mode = \"0640\"\ndir_mode = \"0750\"\n")
	if err != nil || c.FileMode != 0o640 || c.DirMode != 0o750 {
		t.Fatalf("the modes set are %o and %o: %v", c.FileMode, c.DirMode, err)
	}
	for _, bad := range []string{`file_mode = "rw"`, `dir_mode = "1777"`, `file_mode = "888"`} {
		var cerr *Error
		if _, err := Parse(valid + bad + "\n"); !errors.As(err, &cerr) || !strings.Contains(cerr.Reason, "octal") {
			t.Fatalf("%s got %v, want an error that names the mode", bad, err)
		}
	}
}

func TestParseWithoutAClientFile(t *testing.T) {
	c, err := Parse(strings.Replace(valid, "client_id_file = \"/etc/fastgd/client_id.json\"\n", "", 1))
	if err != nil || c.ClientIDFile != "" {
		t.Fatalf("without client_id_file: %+v, %v", c, err)
	}
}
