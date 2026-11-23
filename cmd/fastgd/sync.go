package main

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func syncCmd(args []string) error {
	cfg, err := loadConfig("sync", args)
	if err != nil {
		return err
	}
	got, err := ask(controlSocket(cfg.CacheDir), "sync")
	if err != nil {
		return err
	}
	left, err := parseSyncAnswer(got)
	if err != nil {
		return err
	}
	if left == 0 {
		fmt.Fprintln(os.Stdout, "everything is in Drive")
		return nil
	}

	if db, st, err := readStatus(cfg.CacheDir, time.Now()); err == nil {
		db.Close()
		printFailing(os.Stdout, st)
	}
	return fmt.Errorf("operations left that Drive did not take: %d; fastgd status tells more", left)
}

func printFailing(w io.Writer, st meta.Status) {
	for _, f := range st.Failing {
		fmt.Fprintf(w, "%s: %s\n", plain(f.Path), plain(f.LastError))
	}
}
