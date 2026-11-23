package emulator

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	drive "google.golang.org/api/drive/v3"
)

func (e *Emulator) routes() {
	m := http.NewServeMux()
	m.HandleFunc("GET /drive/v3/files", e.handleList)
	m.HandleFunc("GET /drive/v3/files/{id}", e.handleGet)
	m.HandleFunc("GET /drive/v3/changes/startPageToken", e.handleStartPageToken)
	m.HandleFunc("GET /drive/v3/changes", e.handleChanges)
	m.HandleFunc("GET /drive/v3/files/generateIds", e.handleGenerateIDs)
	m.HandleFunc("POST /drive/v3/files", e.handleCreate)
	m.HandleFunc("POST /upload/drive/v3/files", e.handleCreate)
	m.HandleFunc("PATCH /upload/drive/v3/files/{id}", e.handleUploadUpdate)
	m.HandleFunc("PUT /upload/drive/v3/files", e.handleChunk)
	m.HandleFunc("PUT /upload/drive/v3/files/{id}", e.handleChunk)
	m.HandleFunc("PATCH /drive/v3/files/{id}", e.handlePatch)
	m.HandleFunc("GET /drive/v3/about", e.handleAbout)
	m.HandleFunc("GET /drive/v3/files/{id}/revisions/{rev}", e.handleRevisionGet)
	m.HandleFunc("PATCH /drive/v3/files/{id}/revisions/{rev}", e.handleRevisionPatch)
	e.mux = m
}

func (e *Emulator) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if e.offline.Load() {

		panic(http.ErrAbortHandler)
	}
	e.mu.Lock()
	if r.Method != http.MethodGet || strings.HasSuffix(r.URL.Path, "/generateIds") {
		e.writes.note(time.Now())
	} else {
		e.reads.note(time.Now())
	}
	e.requests = append(e.requests, Request{
		Method:       r.Method,
		Path:         r.URL.Path,
		Query:        r.URL.Query(),
		Range:        r.Header.Get("Range"),
		ContentRange: r.Header.Get("Content-Range"),
	})
	e.mu.Unlock()
	if d := time.Duration(e.uploadDelay.Load()); d > 0 && strings.HasPrefix(r.URL.Path, "/upload/") {
		e.answerLate(w, r, d)
		return
	}
	e.serve(w, r)
}

func (e *Emulator) serve(w http.ResponseWriter, r *http.Request) {
	if e.refuse(w, r) {
		return
	}
	e.mux.ServeHTTP(w, r)
}

func (e *Emulator) target(w http.ResponseWriter, id string) (*File, bool) {
	f, ok := e.files[id]
	if !ok {
		notFound(w, id)
		return nil, false
	}
	if no := e.injected(f.Name); no != nil {
		no.write(w)
		return nil, false
	}
	return f, true
}

const defaultPageSize = 100

func pageSize(w http.ResponseWriter, q url.Values) (size int, ok bool) {
	s := q.Get("pageSize")
	if s == "" {
		return defaultPageSize, true
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 1000 {
		writeError(w, http.StatusBadRequest, "invalid", "bad pageSize "+s)
		return 0, false
	}
	return n, true
}

func notFound(w http.ResponseWriter, id string) {
	writeError(w, http.StatusNotFound, "notFound", "File not found: "+id)
}

func writeError(w http.ResponseWriter, code int, reason, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)

	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":    code,
			"message": message,
			"errors":  []map[string]string{{"domain": "global", "reason": reason, "message": message}},
		},
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")

	_ = json.NewEncoder(w).Encode(v)
}

func formatTime(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000Z") }

func (e *Emulator) apiFile(f *File) *drive.File {
	df := &drive.File{
		Kind:              "drive#file",
		Id:                f.ID,
		Name:              f.Name,
		MimeType:          f.MimeType,
		Parents:           append([]string(nil), f.Parents...),
		Trashed:           e.trashed(f),
		ExplicitlyTrashed: f.Trashed,
		Version:           f.Version,
		CreatedTime:       formatTime(f.Created),
		ModifiedTime:      formatTime(f.Modified),
	}
	if !f.isGoogleType() {
		df.Size = int64(len(f.Content))
		df.Md5Checksum = f.md5
		df.HeadRevisionId = f.HeadRevision
		df.ForceSendFields = []string{"Size"}
	}
	df.Capabilities = apiCapabilities(f)
	if f.isGoogleType() {
		df.WebViewLink = ViewLink(f.MimeType, f.ID)
	}
	if f.MimeType == MimeShortcut {
		sd := &drive.FileShortcutDetails{TargetId: f.ShortcutTarget}
		if t, ok := e.files[f.ShortcutTarget]; ok {
			sd.TargetMimeType = t.MimeType
		}
		df.ShortcutDetails = sd
	}
	return df
}

func apiCapabilities(f *File) *drive.FileCapabilities {
	caps := &drive.FileCapabilities{
		CanModifyContent:       !f.CannotModify,
		CanDownload:            !f.CannotDownload,
		CanTrash:               !f.CannotTrash,
		CanRename:              !f.CannotRename,
		CanMoveItemWithinDrive: !f.CannotMoveWithinDrive,
		CanReadRevisions:       !f.CannotReadRevisions,
		CanRemoveMyDriveParent: !f.CannotMove,

		CanAddMyDriveParent: len(f.Parents) == 0 && !f.CannotMove,
		CanAddChildren:      f.MimeType == MimeFolder && !f.CannotAddChildren,
		CanRemoveChildren:   f.MimeType == MimeFolder && !f.CannotRemoveChildren,
	}

	for _, r := range []struct {
		field, name string
		v           *bool
	}{
		{"CanDownload", "canDownload", &caps.CanDownload}, {"CanModifyContent", "canModifyContent", &caps.CanModifyContent},
		{"CanTrash", "canTrash", &caps.CanTrash}, {"CanRename", "canRename", &caps.CanRename},
		{"CanMoveItemWithinDrive", "canMoveItemWithinDrive", &caps.CanMoveItemWithinDrive},
		{"CanRemoveMyDriveParent", "canRemoveMyDriveParent", &caps.CanRemoveMyDriveParent},
		{"CanAddMyDriveParent", "canAddMyDriveParent", &caps.CanAddMyDriveParent},
		{"CanReadRevisions", "canReadRevisions", &caps.CanReadRevisions},
		{"CanAddChildren", "canAddChildren", &caps.CanAddChildren}, {"CanRemoveChildren", "canRemoveChildren",
			&caps.CanRemoveChildren},
	} {
		if slices.Contains(f.RightsLeftOut, r.name) {
			*r.v = false
			continue
		}
		caps.ForceSendFields = append(caps.ForceSendFields, r.field)
	}
	return caps
}

func (e *Emulator) handleGet(w http.ResponseWriter, r *http.Request) {
	id, a := e.lookup(r)
	switch {
	case !a.ok:
		notFound(w, id)
	case !a.media:
		writeJSON(w, a.meta)
	case a.google:
		writeError(w, http.StatusForbidden, "fileNotDownloadable", "Only files with binary content can be downloaded")
	case a.forbidden:
		writeError(w, http.StatusForbidden, "cannotDownloadFile", "This file cannot be downloaded by the user.")
	case a.abusive:
		writeAbusive(w)
	case a.overQuota:
		writeDownloadQuota(w)
	default:
		e.serveMedia(w, r, a.content)
	}
}

func (e *Emulator) handleList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	onlyLive, ok := listFilter(w, q)
	if !ok {
		return
	}
	size, ok := pageSize(w, q)
	if !ok {
		return
	}
	start, ok := listStart(w, q)
	if !ok {
		return
	}

	e.mu.Lock()
	hook, n := e.onListPage, e.listPages
	e.listPages++
	e.mu.Unlock()
	if hook != nil {
		hook(n)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	writeJSON(w, e.listFrom(start, size, onlyLive))
}

func (e *Emulator) handleStartPageToken(w http.ResponseWriter, _ *http.Request) {
	e.mu.Lock()
	defer e.mu.Unlock()
	writeJSON(w, &drive.StartPageToken{Kind: "drive#startPageToken", StartPageToken: strconv.Itoa(len(e.changes) + 1)})
}
