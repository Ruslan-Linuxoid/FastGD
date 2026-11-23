package binds

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func (h *helper) openPlace(target string) (*os.File, error) {
	name := filepath.Base(target)
	if !filepath.IsAbs(target) || target != filepath.Clean(target) || name == "/" || name == "." || name == ".." {
		return nil, fmt.Errorf("%s: %w: not a clean absolute path", target, ErrPlaceTaken)
	}
	above, err := h.openAbove(target)
	if err != nil {
		return nil, err
	}
	defer above.Close()
	place, err := openOrMake(above, name, target)
	if err != nil {
		return nil, err
	}
	if h.beforeCheck != nil {
		h.beforeCheck()
	}
	if err := h.checkFree(above, place, target); err != nil {
		place.Close()
		return nil, err
	}
	return place, nil
}

func statxPlace(fd int, st *unix.Statx_t) error {
	return unix.Statx(fd, "", unix.AT_EMPTY_PATH, unix.STATX_MNT_ID|unix.STATX_NLINK, st)
}

const placeMode = 0o755

func openOrMake(above *os.File, name, target string) (*os.File, error) {
	const flags = unix.O_PATH | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC
	fd, err := unix.Openat(int(above.Fd()), name, flags, 0)
	if errors.Is(err, unix.ENOENT) {
		if unix.Mkdirat(int(above.Fd()), name, placeMode) != nil {
			return nil, fmt.Errorf("%s: %w: cannot make it", target, ErrPlaceTaken)
		}
		fd, err = unix.Openat(int(above.Fd()), name, flags, 0)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w: not a folder", target, ErrPlaceTaken)
	}
	return os.NewFile(uintptr(fd), target), nil
}

func (h *helper) openAbove(target string) (*os.File, error) {
	top, err := h.mountDev()
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(filepath.Dir(target))
	if err != nil {
		return nil, fmt.Errorf("%s: %w: the folder it would be made in is not there", target, ErrPlaceTaken)
	}
	fd, err := unix.Open(resolved, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("%s: %w: the folder it would be made in is not there", target, ErrPlaceTaken)
	}
	above := os.NewFile(uintptr(fd), resolved)
	var st unix.Stat_t
	_, inMount := h.mountOf(resolved)
	if inMount == nil || within(resolved, h.mountPoint) || unix.Fstat(fd, &st) != nil || st.Dev == top {
		above.Close()
		return nil, fmt.Errorf("%s: %w: it is inside the mount of the daemon", target, ErrPlaceTaken)
	}
	return above, nil
}

func (h *helper) checkFree(above, place *os.File, target string) error {
	var up, in unix.Statx_t
	if h.statx(int(above.Fd()), &up) != nil || h.statx(int(place.Fd()), &in) != nil {
		return fmt.Errorf("%s: %w: cannot be looked at", target, ErrPlaceTaken)
	}
	if up.Mask&unix.STATX_MNT_ID == 0 || in.Mask&unix.STATX_MNT_ID == 0 {
		return fmt.Errorf("%s: %w: the kernel tells no mount of a folder; binding folders needs Linux 5.8 or later",
			target, ErrPlaceTaken)
	}
	if in.Mnt_id != up.Mnt_id {
		return fmt.Errorf("%s: %w: something is mounted there", target, ErrPlaceTaken)
	}
	if in.Nlink == 0 {
		return fmt.Errorf("%s: %w: the folder is removed", target, ErrPlaceTaken)
	}
	fd, err := unix.Openat(int(place.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	dir := os.NewFile(uintptr(fd), target)
	defer dir.Close()
	if _, err := dir.Readdirnames(1); !errors.Is(err, io.EOF) {
		return fmt.Errorf("%s: %w: the folder is not empty", target, ErrPlaceTaken)
	}
	return nil
}
