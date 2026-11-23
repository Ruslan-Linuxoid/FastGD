package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/Ruslan-Linuxoid/FastGD/internal/binds"
	"github.com/Ruslan-Linuxoid/FastGD/internal/config"
)

type binder interface {
	Add(path, target string) (binds.Added, error)
	Remove(target string) error
	List() ([]binds.Listed, error)
}

const mountRequest = "mount "

const (
	opAdd    = "add"
	opRemove = "remove"
	opList   = "list"
)

type mountAsk struct {
	Op     string `json:"op"`
	Path   string `json:"path,omitempty"`
	Target string `json:"target,omitempty"`
}

func answerMount(b binder, body string, log *slog.Logger) string {
	var req mountAsk
	if err := json.Unmarshal([]byte(body), &req); err != nil {
		return "error " + err.Error()
	}
	if b == nil {
		return "error the daemon keeps no bindings"
	}
	switch req.Op {
	case opAdd:
		added, err := b.Add(req.Path, req.Target)
		if err != nil {
			return "error " + err.Error()
		}
		log.Info("a binding was added", "folder", req.Path, "at", req.Target)
		return "ok " + string(added)
	case opRemove:
		if err := b.Remove(req.Target); err != nil {
			return "error " + err.Error()
		}
		log.Info("a binding was removed", "at", req.Target)
		return "ok removed"
	case opList:
		rows, err := b.List()
		if err != nil {
			return "error " + err.Error()
		}
		data, err := json.Marshal(rows)
		if err != nil {
			return "error " + err.Error()
		}
		return "ok " + string(data)
	}
	return fmt.Sprintf("error unknown mount command %q", req.Op)
}

const mountUsage = `usage:
  fastgd mount add [--config file] <folder> <place>
  fastgd mount list [--config file]
  fastgd mount remove [--config file] <place>`

func mountCmd(args []string) error {
	if len(args) == 0 {
		return errors.New(mountUsage)
	}
	cfg, rest, err := loadConfigArgs("mount "+args[0], args[1:], nil)
	if err != nil {
		return err
	}
	req, err := mountAskOf(args[0], rest)
	if err != nil {
		return err
	}
	return askMount(cfg, req, os.Stdout)
}

func mountAskOf(op string, rest []string) (mountAsk, error) {
	req := mountAsk{Op: op}
	switch {
	case op == opAdd && len(rest) == 2:
		req.Path, req.Target = rest[0], rest[1]
	case op == opRemove && len(rest) == 1:
		req.Target = rest[0]
	case op == opList && len(rest) == 0:
		return req, nil
	default:
		return mountAsk{}, errors.New(mountUsage)
	}

	var err error
	req.Target, err = filepath.Abs(req.Target)
	return req, err
}

func askMount(cfg *config.Config, req mountAsk, w io.Writer) error {
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	got, err := ask(controlSocket(cfg.CacheDir), mountRequest+string(data))
	if err != nil {
		return err
	}
	text, ok := strings.CutPrefix(got, "ok ")
	if !ok {
		return fmt.Errorf("the daemon answered: %s", strings.TrimPrefix(got, "error "))
	}
	if req.Op != opList {
		fmt.Fprintln(w, text)
		return nil
	}
	var rows []binds.Listed
	if err := json.Unmarshal([]byte(text), &rows); err != nil {
		return err
	}
	printBindings(w, rows)
	return nil
}

func printBindings(w io.Writer, rows []binds.Listed) {
	for _, r := range rows {
		folder := "/" + r.Path
		if r.Path == "" && r.State == binds.Ignored {
			folder = "-"
		}
		state := string(r.State)
		if r.Reason != "" {
			state += ": " + r.Reason
		}
		fmt.Fprintf(w, "%s\t%s\t%s\n", plain(r.Target), plain(folder), state)
	}
}
