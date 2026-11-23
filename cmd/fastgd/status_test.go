package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func TestBytesText(t *testing.T) {
	for n, want := range map[int64]string{0: "0 B", 1023: "1023 B", 1024: "1.0 KiB", 5 << 30: "5.0 GiB",
		3 << 39: "1.5 TiB"} {
		if got := bytesText(n); got != want {
			t.Errorf("bytesText(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestPrintStatus(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.Local)
	st := meta.Status{DirtyFiles: 2, DirtyBytes: 3 << 20, LostFiles: 4, LastPoll: now.Add(-15 * time.Second)}
	st.Failing = []meta.Failing{{Attempts: 3, NextTry: now.Add(time.Minute), LastError: "503 backend",
		Path: "Docs/stuck.txt"}}
	st.DayBytes, st.DayUnits = 3<<29, 2_000_000
	st.DownloadRefused, st.RefusedPath = now.Add(-time.Minute), "Video/big.mkv"
	st.AbuseRefused, st.AbusivePath = now.Add(-2*time.Minute), "Shared/setup.exe"
	st.Undone, st.UndoneWhat = now.Add(-time.Hour), "Docs/bad.txt: 400 invalid"
	st.UploadsRefused = "Drive answered that its storage is full"
	st.LoginNeeded = now.Add(-3 * time.Minute)
	st.Discarded, st.DiscardedWhat = now.Add(-2*time.Hour), "/cache/untrusted-state-20260102-010405: ahead of Drive"
	var b bytes.Buffer
	printStatus(&b, st, 3, 1<<30, 50<<30, now)
	for _, want := range []string{
		"uploads:      refused, writes of content get a read-only error: Drive answered that its storage is full",
		"the copy is kept in /cache/untrusted-state-20260102-010405: ahead of Drive",
		"state copies: 3 of states set aside lie in the cache directory",
		"2, 3.0 MiB", `lost & found: 4 in "Lost and Found (FastGD)"`, "1.0 GiB of 50.0 GiB", "15s ago",
		"Docs/stuck.txt",
		"sign-in:      refused since " + now.Add(-3*time.Minute).Format(time.DateTime) +
			": run fastgd auth; changes made here wait",
		"undone:       " + now.Add(-time.Hour).Format(time.DateTime) + ", Drive did not take Docs/bad.txt: 400 invalid",
		"attempt 3", "503 backend",
		"downloaded:   1.5 GiB in 24 h, 0.2% of the project's free 931.3 GiB (1 TB)",
		"quota units:  2000000 in 24 h, 0.5% of the project's free 400000000",
		"download:     refused by the download quota at " + now.Add(-time.Minute).Format(time.DateTime) + ": Video/big.mkv",
		"flagged:      Drive gives the content of a file it flagged as abusive to its owner alone, refused at " +
			now.Add(-2*time.Minute).Format(time.DateTime) + ": Shared/setup.exe"} {
		if !strings.Contains(b.String(), want) {
			t.Errorf("status does not show %q:\n%s", want, b.String())
		}
	}
	b.Reset()
	printStatus(&b, meta.Status{}, 0, 0, 50<<30, now)
	if want := "download:     never refused by the download quota"; !strings.Contains(b.String(), want) {
		t.Errorf("status does not show %q:\n%s", want, b.String())
	}
	if strings.Contains(b.String(), "flagged:") {
		t.Errorf("status tells of a flagged file Drive never refused:\n%s", b.String())
	}
}

func TestStatusPrintsNamesWithoutControlCharacters(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.Local)
	st := meta.Status{Failing: []meta.Failing{{Attempts: 1, NextTry: now, LastError: "400 \x1b[2Jbad",
		Path: "Docs/\x1b[31mred\a.txt"}}}
	st.Undone, st.UndoneWhat = now, "Docs/\x1b]0;title\a.txt: 400"
	var b bytes.Buffer
	printStatus(&b, st, 0, 0, 50<<30, now)
	out := b.String()
	if strings.ContainsAny(out, "\x1b\a") {
		t.Fatalf("status prints control characters:\n%q", out)
	}
	for _, want := range []string{"Docs/?[31mred?.txt", "400 ?[2Jbad", "Docs/?]0;title?.txt: 400"} {
		if !strings.Contains(out, want) {
			t.Errorf("status does not show %q:\n%s", want, out)
		}
	}
}

func TestSyncPrintsNamesWithoutControlCharacters(t *testing.T) {
	st := meta.Status{Failing: []meta.Failing{{LastError: "400 \x1b[2Jbad", Path: "Docs/\x1b[31mred\a.txt"}}}
	var b bytes.Buffer
	printFailing(&b, st)
	if out := b.String(); strings.ContainsAny(out, "\x1b\a") || !strings.Contains(out, "Docs/?[31mred?.txt: 400 ?[2Jbad") {
		t.Fatalf("sync prints %q", out)
	}
}

func TestStatusShowsTheLimitTheCacheEvictsBy(t *testing.T) {
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.Local)
	var b bytes.Buffer
	printStatus(&b, meta.Status{CacheLimit: 1 << 30, CacheLimitBy: "drive"}, 0, 0, 10<<30, now)
	if want := "cache:        0 B of 1.0 GiB, set by the files in Drive"; !strings.Contains(b.String(), want) {
		t.Errorf("status does not show %q:\n%s", want, b.String())
	}
	b.Reset()
	printStatus(&b, meta.Status{}, 0, 0, 10<<30, now)
	if want := "cache:        0 B of 10.0 GiB, set by cache_size"; !strings.Contains(b.String(), want) {
		t.Errorf("status does not show %q:\n%s", want, b.String())
	}
}
