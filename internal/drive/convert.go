package drive

import (
	"fmt"
	"regexp"

	"google.golang.org/api/drive/v3"
)

var validID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func ValidID(id string) bool { return validID.MatchString(id) }

type BadIDError struct{ ID string }

func (e *BadIDError) Error() string { return fmt.Sprintf("an answer holds %q for a fileId", e.ID) }

func convert(f *drive.File) (File, error) {
	if !ValidID(f.Id) {
		return File{}, &BadIDError{ID: f.Id}
	}
	if f.DriveId != "" && !ValidID(f.DriveId) {
		return File{}, &BadIDError{ID: f.DriveId}
	}
	out := File{
		ID:           f.Id,
		Name:         f.Name,
		MimeType:     f.MimeType,
		Parents:      f.Parents,
		DriveID:      f.DriveId,
		Shared:       f.SharedWithMeTime != "",
		Trashed:      f.Trashed,
		OwnTrashed:   f.ExplicitlyTrashed,
		Size:         f.Size,
		MD5:          f.Md5Checksum,
		HeadRevision: f.HeadRevisionId,
		Version:      f.Version,
	}
	var err error
	if out.Modified, err = parseTime(f.ModifiedTime); err != nil {
		return File{}, fmt.Errorf("drive: %s modifiedTime: %w", f.Id, err)
	}
	if out.Created, err = parseTime(f.CreatedTime); err != nil {
		return File{}, fmt.Errorf("drive: %s createdTime: %w", f.Id, err)
	}
	if f.ShortcutDetails != nil {
		out.ShortcutTarget = f.ShortcutDetails.TargetId
	}
	out.ViewLink = f.WebViewLink
	out.Can = rightsOf(f)
	if c := f.Capabilities; c != nil && !c.CanReadRevisions {

		out.HeadRevision = ""
	}
	return out, nil
}

func rightsOf(f *drive.File) Rights {
	c := f.Capabilities
	if c == nil {
		return Rights{ModifyContent: true, Trash: true, Rename: true, Move: true, AddChildren: true,
			RemoveChildren: true, Download: true, OutOfDrive: true}
	}
	can := Rights{
		ModifyContent: c.CanModifyContent,
		Trash:         c.CanTrash,
		Rename:        c.CanRename,
		Move:          c.CanRemoveMyDriveParent,
		OutOfDrive:    c.CanMoveItemOutOfDrive,
		AddChildren:   c.CanAddChildren,

		RemoveChildren: c.CanRemoveChildren || f.MimeType != MimeFolder,

		Download: c.CanDownload || f.MimeType == MimeFolder,
	}
	if f.DriveId != "" {

		can.Move = c.CanMoveItemWithinDrive
	}
	return can
}
