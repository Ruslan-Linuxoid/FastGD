package emulator

import (
	"net/http"
	"slices"
	"strconv"

	drive "google.golang.org/api/drive/v3"
)

type change struct {
	id      string
	removed bool

	drive bool
}

func (e *Emulator) Update(id string, fn func(*File)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	f, ok := e.files[id]
	if !ok {
		return
	}
	c := f.bare()
	c.Modified = e.now()
	fn(&c)
	c.Content = slices.Clone(c.Content)
	c.md5 = md5Hex(c.Content)
	e.revise(&c)
	c.Version = f.Version + 1
	e.files[id] = &c
	e.changes = append(e.changes, change{id: id})
}

func (e *Emulator) Delete(id string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.files, id)
	e.changes = append(e.changes, change{id: id, removed: true})
}

func (e *Emulator) handleChanges(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	tok, err := strconv.Atoi(q.Get("pageToken"))
	if err != nil || tok < 1 {
		writeError(w, http.StatusBadRequest, "invalid", "bad pageToken "+q.Get("pageToken"))
		return
	}
	size, ok := pageSize(w, q)
	if !ok {
		return
	}
	myDrive := flag(q, "restrictToMyDrive")
	withRemoved := q.Get("includeRemoved") != "false"
	allDrives := allDrivesAsked(q)

	e.mu.Lock()
	defer e.mu.Unlock()
	out := &drive.ChangeList{Kind: "drive#changeList", Changes: []*drive.Change{}}
	now := formatTime(e.now())
	i := tok - 1
	for ; i < len(e.changes) && len(out.Changes) < size; i++ {
		if ch := e.changeEntry(e.changes[i], listing{myDrive, withRemoved, allDrives}, now); ch != nil {
			out.Changes = append(out.Changes, ch)
		}
	}
	if i < len(e.changes) {
		out.NextPageToken = strconv.Itoa(i + 1)
	} else {
		out.NewStartPageToken = strconv.Itoa(len(e.changes) + 1)
	}
	writeJSON(w, out)
}

type listing struct{ myDrive, withRemoved, allDrives bool }

func (e *Emulator) changeEntry(c change, l listing, now string) *drive.Change {
	if c.drive {
		return e.driveChange(c, l, now)
	}
	f, ok := e.files[c.id]
	if c.removed || !ok {
		if !l.withRemoved {
			return nil
		}
		return removedChange(c.id, now)
	}
	if f.DriveID != "" && (!l.allDrives || l.myDrive) {
		return nil
	}
	if l.myDrive && len(f.Parents) == 0 {
		return e.parentlessEntry(f, now)
	}
	return &drive.Change{Kind: changeKind, ChangeType: fileChange, FileId: c.id, File: e.apiFile(f), Time: now}
}

func (e *Emulator) parentlessEntry(f *File, now string) *drive.Change {
	switch e.parentless {
	case ParentlessLeftOut:

		return nil
	case ParentlessRemoved:
		return removedChange(f.ID, now)
	case ParentlessShown:
	}
	return &drive.Change{Kind: changeKind, ChangeType: fileChange, FileId: f.ID, File: e.apiFile(f), Time: now}
}

func removedChange(id, now string) *drive.Change {
	return &drive.Change{Kind: changeKind, ChangeType: fileChange, FileId: id, Removed: true, Time: now}
}

type Parentless int

const (
	ParentlessLeftOut Parentless = iota

	ParentlessRemoved

	ParentlessShown
)

func (e *Emulator) TellParentless(how Parentless) { e.locked(func() { e.parentless = how }) }
