package binds

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
)

const HelperFD = 3

type Helper struct {
	mu   sync.Mutex
	conn *os.File
	enc  *json.Encoder
	dec  *json.Decoder
}

func StartHelper(prog string, args ...string) (*Helper, error) {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM|syscall.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, fmt.Errorf("binds: %w", err)
	}
	ours, theirs := os.NewFile(uintptr(fds[0]), "helper"), os.NewFile(uintptr(fds[1]), "daemon")
	defer theirs.Close()

	cmd := exec.Command(prog, args...)
	cmd.ExtraFiles = []*os.File{theirs}
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		ours.Close()
		return nil, fmt.Errorf("binds: start the helper: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	return &Helper{conn: ours, enc: json.NewEncoder(ours), dec: json.NewDecoder(ours)}, nil
}

func (h *Helper) Bind(source, target string) error {
	return h.ask(request{Source: source, Target: target})
}

func (h *Helper) Unbind(target string) error { return h.ask(request{Target: target}) }

func (h *Helper) Close() error { return h.conn.Close() }

func (h *Helper) ask(req request) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if err := h.enc.Encode(req); err != nil {
		return fmt.Errorf("binds: the helper: %w", err)
	}
	var rep reply
	if err := h.dec.Decode(&rep); err != nil {
		return fmt.Errorf("binds: the helper: %w", err)
	}
	if rep.Error != "" {
		return errors.New(rep.Error)
	}
	return nil
}
