package emulator

import (
	"net/http"
	"net/url"
	"strings"

	drive "google.golang.org/api/drive/v3"
)

const (
	changeKind = "drive#change"
	fileChange = "file"
)

func flag(q url.Values, name string) bool { return q.Get(name) == wireTrue }

const wireTrue = "true"

func allDrivesAsked(q url.Values) bool {
	return flag(q, "includeItemsFromAllDrives") && flag(q, "supportsAllDrives")
}

func (e *Emulator) AddSharedDrive(name string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	id := e.newID()
	now := e.now()
	e.files[id] = &File{ID: id, Name: name, MimeType: MimeFolder, DriveID: id, driveRoot: true, Version: 1,
		Created: now, Modified: now}
	e.order = append(e.order, id)
	e.changes = append(e.changes, change{id: id, drive: true})
	return id
}

func (e *Emulator) RenameSharedDrive(id, name string) {
	e.UpdateSharedDrive(id, func(f *File) { f.Name = name })
}

func (e *Emulator) UpdateSharedDrive(id string, fn func(*File)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if f, ok := e.files[id]; ok && f.driveRoot {
		fn(f)
		e.changes = append(e.changes, change{id: id, drive: true})
	}
}

func apiDrive(f *File) *drive.Drive {
	return &drive.Drive{Kind: "drive#drive", Id: f.ID, Name: f.Name, Capabilities: &drive.DriveCapabilities{
		CanAddChildren: !f.CannotAddChildren, CanEdit: !f.CannotModify, CanDownload: !f.CannotDownload,
		CanTrashChildren: !f.CannotRemoveChildren, CanDeleteChildren: !f.CannotRemoveChildren,
		ForceSendFields: []string{"CanAddChildren", "CanEdit", "CanDownload", "CanTrashChildren", "CanDeleteChildren"},
	}}
}

func (e *Emulator) RemoveSharedDrive(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for fid, f := range e.files {
		if f.DriveID == id && !f.driveRoot {
			delete(e.files, fid)
			e.changes = append(e.changes, change{id: fid, removed: true})
		}
	}
	delete(e.files, id)
	e.changes = append(e.changes, change{id: id, removed: true, drive: true})
}

func (e *Emulator) driveOf(parents []string) string {
	for _, p := range parents {
		if pf, ok := e.files[p]; ok && pf.DriveID != "" {
			return pf.DriveID
		}
	}
	return ""
}

func (e *Emulator) driveChange(c change, l listing, now string) *drive.Change {
	if !l.allDrives || l.myDrive {
		return nil
	}
	ch := &drive.Change{Kind: changeKind, ChangeType: "drive", DriveId: c.id, Time: now}
	if f, ok := e.files[c.id]; ok && !c.removed && f.driveRoot {
		ch.Drive = apiDrive(f)
	} else {
		ch.Removed = true
	}
	return ch
}

func (e *Emulator) handleDrives(w http.ResponseWriter, _ *http.Request) {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := &drive.DriveList{Kind: "drive#driveList", Drives: []*drive.Drive{}}
	for _, id := range e.order {
		if f, ok := e.files[id]; ok && f.driveRoot {
			out.Drives = append(out.Drives, apiDrive(f))
		}
	}
	writeJSON(w, out)
}

func (e *Emulator) unsupported(w http.ResponseWriter, r *http.Request) bool {

	if q := r.URL.Query(); flag(q, "supportsAllDrives") || q.Has("upload_id") {
		return false
	}
	_, rest, ok := strings.Cut(r.URL.Path, "/files/")
	if !ok {
		return false
	}
	id, sub, _ := strings.Cut(rest, "/")

	if strings.HasPrefix(sub, "revisions") {
		return false
	}
	e.mu.Lock()
	f, found := e.files[id]
	inDrive := found && f.DriveID != ""
	e.mu.Unlock()
	if inDrive {
		notFound(w, id)
	}
	return inDrive
}
