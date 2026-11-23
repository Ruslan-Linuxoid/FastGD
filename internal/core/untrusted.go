package core

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func untrustedMark(cacheDir string) string { return filepath.Join(cacheDir, "untrusted") }

const copyPrefix = "untrusted-state-"

func markUntrusted(cacheDir, why string) error {
	fd, err := os.OpenFile(untrustedMark(cacheDir), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	_, err = fd.WriteString(why + "\n")
	return errors.Join(err, fd.Sync(), fd.Close())
}

var ErrUntrusted = errors.New("the state is not to be trusted and is set aside at the next start")

func (f *FS) distrust(why string) {
	f.distrustOnce.Do(func() {
		if err := markUntrusted(f.opt.CacheDir, why); err != nil {
			f.log.Error("mark the state as not to be trusted", "err", err)
		}
		f.log.Error("the state is not to be trusted: the daemon stops, and its next start sets the state aside and "+
			"restores from the copy what did not reach Drive", "why", why)
		close(f.distrusted)
	})
}

func (f *FS) Distrusted() <-chan struct{} { return f.distrusted }

func StateCopies(cacheDir string) []string {

	copies, _ := filepath.Glob(filepath.Join(cacheDir, copyPrefix+"*"))
	slices.Sort(copies)
	return copies
}

func setAside(cacheDir string, now time.Time) (aside, why string, err error) {
	why, begun, found, err := readMark(cacheDir)
	if !found || err != nil {
		return "", "", err
	}
	state, err := stateEntries(cacheDir)
	if err != nil {
		return "", "", err
	}
	if aside, err = pickCopy(cacheDir, now, why, begun, state); aside == "" || err != nil {
		return "", "", err
	}
	if err := moveState(cacheDir, aside, state); err != nil {
		return "", "", err
	}
	return aside, why, nil
}

func readMark(cacheDir string) (why, begun string, found bool, err error) {
	text, err := os.ReadFile(untrustedMark(cacheDir))
	if errors.Is(err, fs.ErrNotExist) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	why, begun, _ = strings.Cut(strings.TrimSpace(string(text)), "\n"+markCopy)
	return why, begun, true, nil
}

func stateEntries(cacheDir string) ([]string, error) {
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		return nil, err
	}
	var state []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), filepath.Base(DBPath(cacheDir))) || e.Name() == filepath.Base(ContentDir(cacheDir)) {
			state = append(state, e.Name())
		}
	}
	return state, nil
}

func pickCopy(cacheDir string, now time.Time, why, begun string, state []string) (string, error) {
	switch {
	case begun != "":

		if begun != filepath.Base(begun) || !strings.HasPrefix(begun, copyPrefix) {
			return "", fmt.Errorf("the mark %s names %q, which is no copy of a state", untrustedMark(cacheDir), begun)
		}
		return filepath.Join(cacheDir, begun), nil
	case len(state) == 0:

		return "", os.Remove(untrustedMark(cacheDir))
	}

	aside := newCopy(cacheDir, now)
	named := why + "\n" + markCopy + filepath.Base(aside) + "\n"
	return aside, writeMark(cacheDir, named)
}

func moveState(cacheDir, aside string, state []string) error {
	if err := os.MkdirAll(aside, 0o700); err != nil {
		return err
	}
	for _, name := range state {
		if err := os.Rename(filepath.Join(cacheDir, name), filepath.Join(aside, name)); err != nil {
			return err
		}
	}

	return os.Remove(untrustedMark(cacheDir))
}

const markCopy = "copy: "

func writeMark(cacheDir, text string) error {
	tmp := untrustedMark(cacheDir) + ".new"

	fd, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, err = fd.WriteString(text)
	if err = errors.Join(err, fd.Sync(), fd.Close()); err != nil {
		return err
	}
	return os.Rename(tmp, untrustedMark(cacheDir))
}

func newCopy(cacheDir string, now time.Time) string {
	name := filepath.Join(cacheDir, copyPrefix+now.UTC().Format("20060102-150405"))
	for n := 1; ; n++ {
		aside := name
		if n > 1 {
			aside = fmt.Sprintf("%s-%03d", name, n)
		}
		if _, err := os.Lstat(aside); err != nil {
			return aside
		}
	}
}

func aheadOfDrive(have, next string) (string, bool) {
	here, err := strconv.ParseInt(have, 10, 64)
	if err != nil {
		return "", false
	}
	there, err := strconv.ParseInt(next, 10, 64)
	if err != nil || here <= there {
		return "", false
	}
	return fmt.Sprintf("the last change known here is %d, and Drive's last is %d", here, there), true
}

func unrecordedCopy(cacheDir string, store *meta.Store) (string, error) {
	if _, told, err := store.GetState(meta.StateDiscarded); err != nil || told {
		return "", err
	}
	had := StateCopies(cacheDir)
	if len(had) == 0 {
		return "", nil
	}
	return had[len(had)-1], nil
}
