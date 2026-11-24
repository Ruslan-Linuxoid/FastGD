package emulator

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	drive "google.golang.org/api/drive/v3"
)

type found struct {
	ok, media, google, forbidden, abusive, overQuota bool
	meta                                             *drive.File
	content                                          []byte
}

func (e *Emulator) lookup(r *http.Request) (string, found) {
	id := r.PathValue("id")
	e.mu.Lock()
	defer e.mu.Unlock()
	if id == "root" {
		id = e.root
	}
	f, ok := e.files[id]

	a := found{ok: ok, media: r.URL.Query().Get("alt") == "media"}
	if ok && !a.media {
		a.meta = e.apiFile(f)
	}
	if ok {

		a.content, a.google = f.Content, f.isGoogleType()
		a.forbidden = f.CannotDownload
		a.abusive = f.abuseRefused(flag(r.URL.Query(), "acknowledgeAbuse"))
	}
	a.overQuota = e.overQuota(id)
	return id, a
}

func listFilter(w http.ResponseWriter, q url.Values) (onlyLive, ok bool) {
	switch strings.ReplaceAll(q.Get("q"), " ", "") {
	case "":
		return false, true
	case "trashed=false":
		return true, true
	default:
		writeError(w, http.StatusBadRequest, "invalidQuery", "emulator does not support q="+q.Get("q"))
		return false, false
	}
}

func listStart(w http.ResponseWriter, q url.Values) (int, bool) {
	t := q.Get("pageToken")
	if t == "" {
		return 0, true
	}
	n, err := strconv.Atoi(t)
	if err != nil || n < 0 {
		writeError(w, http.StatusBadRequest, "invalid", "bad pageToken "+t)
		return 0, false
	}
	return n, true
}

func (e *Emulator) listFrom(start, size int, onlyLive, allDrives bool) *drive.FileList {
	out := &drive.FileList{Kind: "drive#fileList", Files: []*drive.File{}}
	i := start
	for ; i < len(e.order) && len(out.Files) < size; i++ {
		f := e.files[e.order[i]]
		if !e.listed(f, onlyLive, allDrives) {
			continue
		}
		out.Files = append(out.Files, e.apiFile(f))
	}
	if i < len(e.order) {
		out.NextPageToken = strconv.Itoa(i)
	}
	return out
}

func (e *Emulator) listed(f *File, onlyLive, allDrives bool) bool {
	return f != nil && f.ID != e.root && !f.driveRoot && (!onlyLive || !e.trashed(f)) && (f.DriveID == "" || allDrives)
}
