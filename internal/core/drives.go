package core

import (
	"context"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/meta"
)

func driveRootChange(ch drive.Change) drive.Change {
	if ch.Removed {
		return drive.Change{FileID: ch.Drive.ID, Removed: true}
	}
	return drive.Change{FileID: ch.Drive.ID, File: &drive.File{
		ID:       ch.Drive.ID,
		Name:     ch.Drive.Name,
		MimeType: drive.MimeFolder,
		Parents:  []string{meta.SharedDrivesID},
		DriveID:  ch.Drive.ID,
		Can:      ch.Drive.Can,
	}}
}

func (f *FS) driveChanges(ctx context.Context) ([]drive.Change, error) {
	drives, err := f.opt.Drive.Drives(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]drive.Change, 0, len(drives))
	for _, d := range drives {
		out = append(out, driveRootChange(drive.Change{Drive: &d}))
	}
	return out, nil
}

func (f *FS) loadDrives(ctx context.Context) error {
	drives, err := f.driveChanges(ctx)
	if err != nil {
		return err
	}
	token, _, err := f.store.GetState(meta.StateChangesToken)
	if err != nil {
		return err
	}
	f.pollMu.Lock()
	defer f.pollMu.Unlock()
	return f.applyChanges(ctx, drives, token)
}

func (f *FS) rereadDrives(ctx context.Context, listed map[string]bool, old string) error {
	drives, err := f.driveChanges(ctx)
	if err != nil {
		return err
	}
	for _, d := range drives {
		listed[d.FileID] = true
	}
	return f.applyChanges(ctx, drives, old)
}
