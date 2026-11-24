package emulator

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	drive "google.golang.org/api/drive/v3"
)

func (e *Emulator) handlePatch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	req, ok := readPatch(w, r)
	if !ok {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	old, ok := e.target(w, id)
	if !ok {
		return
	}
	f := old.bare()
	if !req.setFields(w, e, old, &f) || !e.setParents(w, req.q, &f) {
		return
	}
	if len(f.Parents) > 1 {
		writeError(w, http.StatusForbidden, "teamDrivesParentLimit", "A file in My Drive can have only one parent.")
		return
	}
	f.Version = old.Version + 1
	e.files[id] = &f
	e.changes = append(e.changes, change{id: id})
	e.answer(w, &f)
}

type patchRequest struct {
	meta drive.File

	raw map[string]json.RawMessage
	q   url.Values
}

func readPatch(w http.ResponseWriter, r *http.Request) (*patchRequest, bool) {
	req := &patchRequest{q: r.URL.Query()}
	if r.ContentLength != 0 {
		body, err := io.ReadAll(r.Body)
		if err == nil {
			err = json.Unmarshal(body, &req.raw)
		}
		if err == nil {
			err = json.Unmarshal(body, &req.meta)
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "badRequest", "bad metadata: "+err.Error())
			return nil, false
		}
	}
	return req, true
}

func (req *patchRequest) setFields(w http.ResponseWriter, e *Emulator, old, f *File) bool {
	if !req.setNameAndTrash(w, old, f) {
		return false
	}
	if (req.q.Get("addParents") != "" || req.q.Get("removeParents") != "") && old.CannotMove {
		denied().write(w)
		return false
	}
	return req.setModified(w, e, old, f)
}

func (req *patchRequest) setNameAndTrash(w http.ResponseWriter, old, f *File) bool {
	meta := &req.meta
	if _, ok := req.raw["name"]; ok {
		if meta.Name != old.Name && old.CannotRename {
			denied().write(w)
			return false
		}
		f.Name = meta.Name
	}
	if _, ok := req.raw["trashed"]; ok {
		if meta.Trashed && !old.Trashed && old.CannotTrash {
			denied().write(w)
			return false
		}
		f.Trashed = meta.Trashed
	}
	return true
}

func (req *patchRequest) setModified(w http.ResponseWriter, e *Emulator, old, f *File) bool {
	if _, ok := req.raw["modifiedTime"]; !ok {
		f.Modified = e.now()
		return true
	}

	t, err := requestTime(req.meta.ModifiedTime)
	if err == nil && old.CannotModify && !t.Equal(old.Modified.Truncate(time.Millisecond)) {
		denied().write(w)
		return false
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "badRequest", "bad modifiedTime")
		return false
	}
	f.Modified = t
	return true
}

func (e *Emulator) setParents(w http.ResponseWriter, q url.Values, f *File) bool {
	if rm := q.Get("removeParents"); rm != "" {
		for p := range strings.SplitSeq(rm, ",") {
			if pf, ok := e.files[p]; ok && pf.CannotRemoveChildren {
				denied().write(w)
				return false
			}
			f.Parents = slices.DeleteFunc(f.Parents, func(x string) bool { return x == p })
		}
	}
	if add := q.Get("addParents"); add != "" {
		for p := range strings.SplitSeq(add, ",") {
			if pf, ok := e.files[p]; !ok || pf.MimeType != MimeFolder {
				notFound(w, p)
				return false
			} else if pf.CannotAddChildren {
				denied().write(w)
				return false
			}
			f.Parents = append(f.Parents, p)
		}
	}
	return e.moveDrive(w, f)
}

func (e *Emulator) moveDrive(w http.ResponseWriter, f *File) bool {
	to := e.driveOf(f.Parents)
	if to == f.DriveID {
		return true
	}
	if f.CannotMoveOutOfDrive {
		denied().write(w)
		return false
	}
	f.DriveID = to
	return true
}
