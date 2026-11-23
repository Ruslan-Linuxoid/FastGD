package cache

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

func (c *Cache) openForWrite(p string) (*os.File, bool, error) {
	c.takePath(p)
	if err := mkdirSynced(filepath.Dir(p)); err != nil {
		return nil, false, err
	}

	fd, err := os.OpenFile(p, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if errors.Is(err, fs.ErrExist) {

		fd, err = os.OpenFile(p, os.O_RDWR, 0)
		return fd, false, err
	}
	if err != nil {
		return nil, false, err
	}
	if err := syncDir(filepath.Dir(p)); err != nil {
		fd.Close()
		return nil, false, err
	}
	return fd, true, nil
}

func mkdirSynced(dir string) error {
	err := os.Mkdir(dir, 0o700)
	if errors.Is(err, fs.ErrExist) {
		return nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		if err := mkdirSynced(filepath.Dir(dir)); err != nil {
			return err
		}
		err = os.Mkdir(dir, 0o700)
	}
	if err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	return syncDir(filepath.Dir(dir))
}

func syncDir(dir string) error {

	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	return err
}
