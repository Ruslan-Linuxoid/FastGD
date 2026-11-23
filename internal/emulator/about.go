package emulator

import (
	"net/http"

	drive "google.golang.org/api/drive/v3"
)

func (e *Emulator) SetQuota(limit, usage int64) {
	e.locked(func() { e.quotaLimit, e.quotaUsage = limit, usage })
}

func (e *Emulator) SetFilesUsage(inDrive, trash int64) {
	e.locked(func() { e.filesUsage, e.trashUsage = inDrive, trash })
}

func (e *Emulator) handleAbout(w http.ResponseWriter, _ *http.Request) {
	e.mu.Lock()
	defer e.mu.Unlock()
	inDrive, trash := e.filesUsage, e.trashUsage
	for _, f := range e.files {
		inDrive += int64(len(f.Content))
		if e.trashed(f) {
			trash += int64(len(f.Content))
		}
	}
	q := &drive.AboutStorageQuota{
		Usage: e.quotaUsage + inDrive, UsageInDrive: inDrive, UsageInDriveTrash: trash,
		ForceSendFields: []string{"Usage", "UsageInDrive", "UsageInDriveTrash"},
	}
	if e.quotaLimit > 0 {
		q.Limit = e.quotaLimit
	}
	writeJSON(w, &drive.About{Kind: "drive#about", StorageQuota: q, User: &drive.User{EmailAddress: Email}})
}
