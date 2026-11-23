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

	myDrive := q.Get("restrictToMyDrive") == "true"
	withRemoved := q.Get("includeRemoved") != "false"

	e.mu.Lock()
	defer e.mu.Unlock()
	out := &drive.ChangeList{Kind: "drive#changeList", Changes: []*drive.Change{}}
	now := formatTime(e.now())
	i := tok - 1
	for ; i < len(e.changes) && len(out.Changes) < size; i++ {
		if ch := e.changeEntry(e.changes[i], myDrive, withRemoved, now); ch != nil {
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

func (e *Emulator) changeEntry(c change, myDrive, withRemoved bool, now string) *drive.Change {
	f, ok := e.files[c.id]
	if c.removed || !ok {
		if !withRemoved {
			return nil
		}
		return removedChange(c.id, now)
	}
	if myDrive && len(f.Parents) == 0 {
		switch e.parentless {
		case ParentlessLeftOut:

			return nil
		case ParentlessRemoved:
			return removedChange(c.id, now)
		case ParentlessShown:
		}
	}
	return &drive.Change{Kind: "drive#change", ChangeType: "file", FileId: c.id, File: e.apiFile(f), Time: now}
}

func removedChange(id, now string) *drive.Change {
	return &drive.Change{Kind: "drive#change", ChangeType: "file", FileId: id, Removed: true, Time: now}
}

type Parentless int

const (
	ParentlessLeftOut Parentless = iota

	ParentlessRemoved

	ParentlessShown
)

func (e *Emulator) TellParentless(how Parentless) { e.locked(func() { e.parentless = how }) }
