package binds

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Ruslan-Linuxoid/FastGD/internal/mounts"
)

var ErrPlaceTaken = errors.New("the place is taken")

func checkPlace(target string) (missing bool, err error) {
	if !filepath.IsAbs(target) {
		return false, fmt.Errorf("%s: %w: not an absolute path", target, ErrPlaceTaken)
	}
	fi, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		parent, perr := os.Stat(filepath.Dir(target))
		if perr != nil || !parent.IsDir() {
			return false, fmt.Errorf("%s: %w: the folder it would be made in is not there", target, ErrPlaceTaken)
		}
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, checkFolder(target, fi)
}

func checkFolder(target string, fi os.FileInfo) error {
	if !fi.IsDir() {
		return fmt.Errorf("%s: %w: not a folder", target, ErrPlaceTaken)
	}
	if types, err := mounts.At(target); err != nil || len(types) > 0 {
		return fmt.Errorf("%s: %w: something is mounted there", target, ErrPlaceTaken)
	}
	if empty, err := isEmpty(target); err != nil || !empty {
		return fmt.Errorf("%s: %w: the folder is not empty", target, ErrPlaceTaken)
	}
	return nil
}

func isEmpty(dir string) (bool, error) {
	f, err := os.Open(dir)
	if err != nil {
		return false, err
	}
	defer f.Close()
	_, err = f.Readdirnames(1)
	if errors.Is(err, io.EOF) {
		return true, nil
	}
	return false, err
}
