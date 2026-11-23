package cache

import (
	"errors"
	"fmt"
	"io/fs"
	"os"

	"golang.org/x/sys/unix"
)

func (c *Cache) punch(p string, s span) error {

	fd, err := os.OpenFile(p, os.O_RDWR, 0)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer fd.Close()
	off := PageDown(s.off)
	return c.opt.punchHoleAt(int(fd.Fd()), off, PageUp(s.end)-off)
}

func punchHole(fd int, off, n int64) error {
	return unix.Fallocate(fd, unix.FALLOC_FL_PUNCH_HOLE|unix.FALLOC_FL_KEEP_SIZE, off, n)
}

const maxUnit = 16 << 20

const probeAt = 2 * maxUnit

func holeUnit(dir string, cut func(fd int, off, n int64) error) (int64, error) {
	fd, err := os.CreateTemp(dir, ".punch-*")
	if err != nil {
		return 0, err
	}
	defer os.Remove(fd.Name())
	defer fd.Close()
	if err := writeProbe(fd); err != nil {
		return 0, err
	}
	data, found, err := probeUnit(dir, fd)
	if err != nil {
		return 0, err
	}

	if err := cut(int(fd.Fd()), data, found); err != nil {
		return 0, fmt.Errorf("%s is on a file system that cannot cut holes into files: %w", dir, err)
	}

	if again, err := unix.Seek(int(fd.Fd()), 0, unix.SEEK_DATA); !errors.Is(err, unix.ENXIO) {

		return 0, fmt.Errorf("%s is on a file system that cuts no hole of %d bytes: data stays at %d (%v)", dir, found, again,
			err)
	}
	return found, nil
}

func writeProbe(fd *os.File) error {
	const length = 4 * maxUnit
	if err := fd.Truncate(length); err != nil {
		return err
	}
	if _, err := fd.WriteAt([]byte{1}, probeAt); err != nil {
		return err
	}
	return fd.Sync()
}

func probeUnit(dir string, fd *os.File) (data, found int64, err error) {
	data, err = unix.Seek(int(fd.Fd()), 0, unix.SEEK_DATA)
	if err != nil {
		return 0, 0, fmt.Errorf("%s is on a file system that does not tell where the holes of a file are: %w", dir, err)
	}
	hole, err := unix.Seek(int(fd.Fd()), data, unix.SEEK_HOLE)
	if err != nil {
		return 0, 0, fmt.Errorf("%s is on a file system that does not tell where the holes of a file are: %w", dir, err)
	}
	found = hole - data
	if data > 2*maxUnit || hole <= 2*maxUnit || !countable(found) || data%found != 0 {
		return 0, 0, fmt.Errorf("%s is on a file system that keeps no holes the cache can count by: one byte written at %d "+
			"of a file of holes shows as data from %d to %d", dir, int64(probeAt), data, hole)
	}
	return data, found, nil
}

func countable(u int64) bool {
	return u >= defaultUnit && u <= maxUnit && u%defaultUnit == 0
}
