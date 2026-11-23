package binds

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/Ruslan-Linuxoid/FastGD/internal/config"
	"github.com/Ruslan-Linuxoid/FastGD/internal/mounts"
)

const MountType = "fuse.fastgd"

type request struct {
	Source string `json:"source,omitempty"`
	Target string `json:"target"`
}

type reply struct {
	Error string `json:"error,omitempty"`
}

type helper struct {
	mountIn string

	mountPoint string
	typesAt    func(dir string) ([]string, error)
	mount      func(source, target string) error
	unmount    func(target string) error

	statx func(fd int, st *unix.Statx_t) error

	beforeOpen, beforeCheck, beforeMount func()
}

func RunHelper(mountIn string, conn io.ReadWriter) error {
	signal.Ignore(syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	h := &helper{
		mountIn: filepath.Clean(mountIn),
		typesAt: mounts.At,
		statx:   statxPlace,
		mount:   func(source, target string) error { return syscall.Mount(source, target, "", syscall.MS_BIND, "") },
		unmount: func(target string) error { return syscall.Unmount(target, syscall.MNT_DETACH) },
	}
	return h.serve(conn)
}

func (h *helper) serve(conn io.ReadWriter) error {
	dec, enc := json.NewDecoder(conn), json.NewEncoder(conn)
	for {
		var req request
		err := dec.Decode(&req)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("binds: a request of the daemon: %w", err)
		}
		var rep reply
		if err := h.do(req); err != nil {
			rep.Error = err.Error()
		}
		if err := enc.Encode(rep); err != nil {
			return fmt.Errorf("binds: an answer to the daemon: %w", err)
		}
	}
}

func (h *helper) do(req request) error {
	if req.Source == "" {
		return h.unbind(req.Target)
	}
	return h.bind(req.Source, req.Target)
}

func (h *helper) bind(source, target string) error {
	mp, err := h.mountOf(source)
	if err != nil {
		return err
	}
	h.mountPoint = mp
	src, err := h.openSource(source)
	if err != nil {
		return err
	}
	defer src.Close()
	place, err := h.openPlace(target)
	if err != nil {
		return err
	}
	defer place.Close()
	if h.beforeMount != nil {
		h.beforeMount()
	}
	return h.mount(fdPath(src), fdPath(place))
}

func fdPath(f *os.File) string { return "/proc/self/fd/" + strconv.Itoa(int(f.Fd())) }

func (h *helper) openSource(source string) (*os.File, error) {
	top, err := h.mountDev()
	if err != nil {
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(source)
	if err != nil {
		return nil, err
	}
	if !within(resolved, h.mountPoint) {
		return nil, fmt.Errorf("%s is not inside the mount of the daemon at %s", source, h.mountPoint)
	}
	if h.beforeOpen != nil {
		h.beforeOpen()
	}
	fd, err := unix.Open(resolved, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", source, err)
	}
	f := os.NewFile(uintptr(fd), resolved)
	var in unix.Stat_t
	if err := unix.Fstat(fd, &in); err != nil || in.Dev != top {
		f.Close()
		return nil, fmt.Errorf("%s is not a folder of the mount of the daemon", source)
	}
	return f, nil
}

func (h *helper) mountDev() (uint64, error) {
	var top unix.Stat_t
	if !h.isOurs(h.mountPoint) || unix.Stat(h.mountPoint, &top) != nil {
		return 0, fmt.Errorf("the mount of the daemon is not up at %s", h.mountPoint)
	}
	return top.Dev, nil
}

func (h *helper) unbind(target string) error {
	if h.isMountFolder(target) || !h.isOurs(target) {
		return fmt.Errorf("nothing of the daemon is mounted at %s", target)
	}
	return h.unmount(target)
}

func (h *helper) isOurs(dir string) bool {
	types, err := h.typesAt(dir)
	return err == nil && len(types) > 0 && types[len(types)-1] == MountType
}

func (h *helper) mountOf(source string) (string, error) {
	resolved, err := filepath.EvalSymlinks(source)
	if err != nil {
		return "", err
	}
	in := h.realIn()
	rel, err := filepath.Rel(in, resolved)
	first, _, _ := strings.Cut(rel, "/")
	if err != nil || !strings.HasPrefix(first, config.MountPrefix) {
		return "", fmt.Errorf("%s is not inside a mount folder of the daemon in %s", source, h.mountIn)
	}
	return filepath.Join(in, first), nil
}

func (h *helper) realIn() string {
	if in, err := filepath.EvalSymlinks(h.mountIn); err == nil {
		return in
	}
	return placeOf(h.mountIn)
}

func (h *helper) isMountFolder(dir string) bool {
	dir = placeOf(dir)
	return filepath.Dir(dir) == h.realIn() && strings.HasPrefix(filepath.Base(dir), config.MountPrefix)
}
