package binds

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type State string

const (
	Bound State = "bound"

	Pending State = "pending"

	Ignored State = "ignored"
)

type Row struct {
	Folder string `json:"folder"`

	Target string `json:"target"`
	State  State  `json:"state"`

	Reason string `json:"reason,omitempty"`
}

const listFile = "mounts.json"

func Load(dir string) ([]Row, error) {
	path := filepath.Join(dir, listFile)
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	var rows []Row
	if err == nil {
		err = json.Unmarshal(data, &rows)
	}
	if err != nil {
		return nil, fmt.Errorf("the list of bindings %s cannot be read: %w; "+
			"delete the file and restart the daemon to start the list anew, and its bindings are lost", path, err)
	}
	return rows, nil
}

func save(dir string, rows []Row) error {
	data, err := json.MarshalIndent(rows, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, listFile+".tmp")
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("binds: %w", err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, listFile)); err != nil {
		return fmt.Errorf("binds: %w", err)
	}
	return nil
}
