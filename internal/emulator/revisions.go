package emulator

import (
	"encoding/json"
	"net/http"
	"strconv"

	drive "google.golang.org/api/drive/v3"
)

type revision struct {
	id      string
	content []byte
	md5     string

	keep bool
}

type Revision struct {
	ID   string
	Keep bool
}

func (e *Emulator) revise(f *File) {
	if f.MimeType == MimeFolder || f.MimeType == MimeShortcut || f.isGoogleType() {
		return
	}
	revs := e.revs[f.ID]
	if n := len(revs); n == 0 || revs[n-1].md5 != f.md5 {
		e.revCount++
		revs = append(revs, &revision{id: "rev" + strconv.Itoa(e.revCount), content: f.Content, md5: f.md5})
		e.revs[f.ID] = revs
	}
	f.HeadRevision = revs[len(revs)-1].id
}

func (e *Emulator) Revisions(id string) []Revision {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Revision, 0, len(e.revs[id]))
	for _, r := range e.revs[id] {
		out = append(out, Revision{ID: r.id, Keep: r.keep})
	}
	return out
}

func (e *Emulator) DeleteRevision(id, rev string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	revs := e.revs[id]
	for i, r := range revs {
		if r.id == rev {
			e.revs[id] = append(revs[:i:i], revs[i+1:]...)
		}
	}
}

func (e *Emulator) revisionOf(id, rev string) *revision {
	for _, r := range e.revs[id] {
		if r.id == rev && e.files[id] != nil {
			return r
		}
	}
	return nil
}

func apiRevision(r *revision) *drive.Revision {
	return &drive.Revision{
		Kind: "drive#revision", Id: r.id, Md5Checksum: r.md5, Size: int64(len(r.content)), KeepForever: r.keep,
		ForceSendFields: []string{"KeepForever", "Size"},
	}
}

func (e *Emulator) handleRevisionGet(w http.ResponseWriter, r *http.Request) {
	e.mu.Lock()
	rev := e.revisionOf(r.PathValue("id"), r.PathValue("rev"))
	var meta *drive.Revision
	var content []byte

	var abusive, forbidden, noRevisions bool
	if f := e.files[r.PathValue("id")]; f != nil {

		abusive = f.abuseRefused(r.URL.Query().Get("acknowledgeAbuse") == "true")
		forbidden = f.CannotDownload
		noRevisions = f.CannotReadRevisions
	}
	overQuota := e.overQuota(r.PathValue("id"))
	if rev != nil {

		meta, content = apiRevision(rev), rev.content
	}
	e.mu.Unlock()
	switch {
	case rev == nil:
		revisionNotFound(w, r)

	case r.URL.Query().Get("alt") != "media":
		writeJSON(w, meta)
	case noRevisions:

		writeError(w, http.StatusForbidden, "downloadRestrictedForRevision",
			"The user cannot download a blob file revision.")
	case forbidden:
		writeError(w, http.StatusForbidden, "cannotDownloadFile", "This file cannot be downloaded by the user.")
	case abusive:
		writeAbusive(w)
	case overQuota:
		writeDownloadQuota(w)
	default:
		e.serveMedia(w, r, content)
	}
}

func (e *Emulator) handleRevisionPatch(w http.ResponseWriter, r *http.Request) {
	var body struct {
		KeepForever *bool `json:"keepForever"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "parseError", err.Error())
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	rev := e.revisionOf(r.PathValue("id"), r.PathValue("rev"))
	if rev == nil {
		revisionNotFound(w, r)
		return
	}
	if body.KeepForever != nil {

		if f := e.files[r.PathValue("id")]; !*body.KeepForever && rev.keep && (f == nil || f.HeadRevision != rev.id) {
			writeError(w, http.StatusBadRequest, "illegalKeepForeverModification",
				"Bad Request. Cannot update a revision to false that is marked as keepForever.")
			return
		}
		rev.keep = *body.KeepForever
	}
	writeJSON(w, apiRevision(rev))
}

func (e *Emulator) KeepRevision(id, rev string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if r := e.revisionOf(id, rev); r != nil {
		r.keep = true
	}
}

func revisionNotFound(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotFound, "notFound", "Revision not found: "+r.PathValue("rev"))
}
