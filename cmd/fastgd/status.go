package main

import (
	"cmp"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode"

	"github.com/Ruslan-Linuxoid/FastGD/internal/binds"
	"github.com/Ruslan-Linuxoid/FastGD/internal/cache"
	"github.com/Ruslan-Linuxoid/FastGD/internal/core"
	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func statusCmd(args []string) error {
	cfg, err := loadConfig("status", args)
	if err != nil {
		return err
	}
	now := time.Now()
	db, st, err := readStatus(cfg.CacheDir, now)
	if err != nil {
		return err
	}
	defer db.Close()

	held, err := cache.Held(db)
	if err != nil {
		return err
	}

	printStatus(os.Stdout, st, len(core.StateCopies(cfg.CacheDir)), held, cfg.CacheSize, now)
	printBindingNotes(os.Stdout, cfg.CacheDir)
	return nil
}

func printBindingNotes(w io.Writer, dir string) {
	rows, err := binds.Load(dir)
	if err != nil {
		fmt.Fprintf(w, "bindings:     %s\n", plain(err.Error()))
	}
	printIgnored(w, rows)
}

func printIgnored(w io.Writer, rows []binds.Row) {
	for _, r := range rows {
		if r.State == binds.Ignored {
			fmt.Fprintf(w, "binding:      %s is ignored, its folder is %s\n", plain(r.Target), r.Reason)
		}
	}
}

func readStatus(dir string, now time.Time) (*meta.ReadOnly, meta.Status, error) {
	db, err := meta.OpenReadOnly(core.DBPath(dir))
	if err != nil {
		return nil, meta.Status{}, fmt.Errorf("%w: the cache is the daemon's, so status runs as the daemon's user or as "+
			"root, and only once the daemon has started", err)
	}
	st, err := db.ReadStatus(now.Add(-drive.QuotaWindow))
	if err != nil {
		db.Close()
		return nil, meta.Status{}, err
	}
	return db, st, nil
}

func when(t time.Time) string { return t.Local().Format(time.DateTime) }

func printStatus(w io.Writer, st meta.Status, copies int, held, size int64, now time.Time) {
	printNotices(w, st, copies)
	printUsage(w, st, held, size, now)
	printFailingOps(w, st)
}

func printNotices(w io.Writer, st meta.Status, copies int) {
	fmt.Fprintf(w, "dirty files:  %d, %s\n", st.DirtyFiles, bytesText(st.DirtyBytes))
	fmt.Fprintf(w, "waiting:      %d operations for Drive\n", st.Waiting)
	fmt.Fprintf(w, "lost & found: %d in %q\n", st.LostFiles, meta.LostName)
	if !st.Discarded.IsZero() {
		fmt.Fprintf(w, "state:        set aside as not to be trusted at %s, the copy is kept in %s\n", when(st.Discarded),
			plain(st.DiscardedWhat))
	}
	if copies > 0 {
		fmt.Fprintf(w, "state copies: %d of states set aside lie in the cache directory\n", copies)
	}
	if !st.Undone.IsZero() {
		fmt.Fprintf(w, "undone:       %s, Drive did not take %s\n", when(st.Undone), plain(st.UndoneWhat))
	}
	if st.UploadsRefused != "" {
		fmt.Fprintf(w, "uploads:      refused, writes of content get a read-only error: %s\n", st.UploadsRefused)
	}
	if !st.LoginNeeded.IsZero() {
		fmt.Fprintf(w, "sign-in:      refused since %s: run fastgd auth; changes made here wait\n",
			when(st.LoginNeeded))
	}
}

const byCacheSize = "cache_size"

var limitCauses = map[string]string{
	byCacheSize: byCacheSize,
	"disk":      "the room on the disk of the cache",
	"drive":     "the files in Drive",
}

func printUsage(w io.Writer, st meta.Status, held, size int64, now time.Time) {
	limit, by := st.CacheLimit, cmp.Or(limitCauses[st.CacheLimitBy], st.CacheLimitBy)
	if st.CacheLimitBy == "" {
		limit, by = size, byCacheSize
	}
	fmt.Fprintf(w, "cache:        %s of %s, set by %s\n", bytesText(held), bytesText(limit), by)
	if st.LastPoll.IsZero() {
		fmt.Fprintln(w, "last poll:    never")
	} else {
		fmt.Fprintf(w, "last poll:    %s, %s ago\n", when(st.LastPoll), now.Sub(st.LastPoll).Round(time.Second))
	}
	fmt.Fprintf(w, "downloaded:   %s in 24 h, %.1f%% of the project's free %s (1 TB)\n",
		bytesText(st.DayBytes), percent(st.DayBytes, drive.FreeBytesPerDay), bytesText(drive.FreeBytesPerDay))
	fmt.Fprintf(w, "quota units:  %d in 24 h, %.1f%% of the project's free %d\n",
		st.DayUnits, percent(st.DayUnits, drive.FreeUnitsPerDay), drive.FreeUnitsPerDay)
	if st.DownloadRefused.IsZero() {
		fmt.Fprintln(w, "download:     never refused by the download quota")
	} else {
		fmt.Fprintf(w, "download:     refused by the download quota at %s: %s\n",
			when(st.DownloadRefused), plain(st.RefusedPath))
	}
	if !st.AbuseRefused.IsZero() {
		fmt.Fprintf(w,
			"flagged:      Drive gives the content of a file it flagged as abusive to its owner alone, refused at %s: %s\n",
			when(st.AbuseRefused), plain(st.AbusivePath))
	}
}

func printFailingOps(w io.Writer, st meta.Status) {
	if len(st.Failing) == 0 {
		fmt.Fprintln(w, "failing:      none")
		return
	}
	fmt.Fprintf(w, "failing:      %d\n", len(st.Failing))
	for _, f := range st.Failing {
		next := f.NextTry
		if next.IsZero() {

			next = time.Unix(0, 0)
		}
		fmt.Fprintf(w, "  %s\n    attempt %d, next at %s: %s\n",
			plain(f.Path), f.Attempts, when(next), plain(f.LastError))
	}
}

func plain(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return '?'
		}
		return r
	}, s)
}

func percent(n, of int64) float64 { return float64(n) * 100 / float64(of) }

func bytesText(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	v, suffix := float64(n), "KMGTPE"
	i := -1
	for v >= unit && i < len(suffix)-1 {
		v /= unit
		i++
	}
	return fmt.Sprintf("%.1f %ciB", v, suffix[i])
}
