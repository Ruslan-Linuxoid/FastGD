package emulator

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strconv"

	drive "google.golang.org/api/drive/v3"
)

type UploadFault func(name string) int

func (e *Emulator) SetUploadFault(fn UploadFault) { e.locked(func() { e.uploadFault = fn }) }

func (e *Emulator) CorruptUploads(n int) { e.locked(func() { e.corrupt = n }) }

func (e *Emulator) LoseUploadAnswers(n int) { e.locked(func() { e.loseAnswers = n }) }

func (e *Emulator) answer(w http.ResponseWriter, f *File) {
	if e.loseAnswers > 0 {
		e.loseAnswers--
		writeError(w, http.StatusServiceUnavailable, "backendError", "the answer was lost")
		return
	}
	writeJSON(w, e.uploaded(f))
}

func (e *Emulator) uploaded(f *File) *drive.File {
	df := e.apiFile(f)
	if e.noUploadMD5 {
		df.Md5Checksum = ""
	}
	return df
}

func (e *Emulator) LeaveMD5OutOfUploadAnswers(on bool) { e.locked(func() { e.noUploadMD5 = on }) }

func (e *Emulator) HoldUploads() (release func()) {
	e.mu.Lock()
	defer e.mu.Unlock()
	ch := make(chan struct{})
	e.hold = ch
	return func() {
		e.mu.Lock()
		if e.hold == ch {
			e.hold = nil
		}
		e.mu.Unlock()
		close(ch)
	}
}

func (e *Emulator) UploadsWaiting() (n int) {
	e.locked(func() { n = e.waiting })
	return n
}

func (e *Emulator) MaxUploadsAtOnce() (n int) {
	e.locked(func() { n = e.maxWaiting })
	return n
}

func (e *Emulator) GiveIDs(ids ...string) { e.locked(func() { e.nextIDs = append(e.nextIDs, ids...) }) }

func (e *Emulator) handleGenerateIDs(w http.ResponseWriter, r *http.Request) {
	n := 10
	if s := r.URL.Query().Get("count"); s != "" {
		c, err := strconv.Atoi(s)
		if err != nil || c < 1 || c > 1000 {
			writeError(w, http.StatusBadRequest, "invalid", "bad count "+s)
			return
		}
		n = c
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	out := &drive.GeneratedIds{Kind: "drive#generatedIds", Space: "drive"}
	for range n {
		id := e.newID()
		if len(e.nextIDs) > 0 {
			id, e.nextIDs = e.nextIDs[0], e.nextIDs[1:]
		}
		e.reserved[id] = true
		out.Ids = append(out.Ids, id)
	}
	writeJSON(w, out)
}

func (e *Emulator) CutSimpleUploads(count, n int) {
	e.locked(func() { e.cutSimples = cuts{count, n} })
}

func (e *Emulator) cutSimple(r *http.Request) {
	if r.URL.Query().Get("uploadType") != "multipart" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if after, ok := e.cutSimples.next(); ok {
		r.Body = &cutBody{r: r.Body, left: after}
	}
}

type cutBody struct {
	r    io.ReadCloser
	left int
}

func (b *cutBody) Read(p []byte) (int, error) {
	if b.left <= 0 {

		panic(http.ErrAbortHandler)
	}
	n, err := b.r.Read(p[:min(len(p), b.left)])
	b.left -= n
	return n, err
}

func (b *cutBody) Close() error { return b.r.Close() }

func readUpload(r *http.Request) (*drive.File, []byte, bool, error) {
	var meta drive.File
	ct, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil && r.ContentLength != 0 {
		return nil, nil, false, fmt.Errorf("bad Content-Type: %w", err)
	}
	if ct != "multipart/related" {
		if r.ContentLength != 0 {
			if err := json.NewDecoder(r.Body).Decode(&meta); err != nil {
				return nil, nil, false, fmt.Errorf("bad metadata: %w", err)
			}
		}
		return &meta, nil, false, nil
	}
	mr := multipart.NewReader(r.Body, params["boundary"])
	part, err := mr.NextPart()
	if err != nil {
		return nil, nil, false, fmt.Errorf("no metadata part: %w", err)
	}
	if err := json.NewDecoder(part).Decode(&meta); err != nil {
		return nil, nil, false, fmt.Errorf("bad metadata: %w", err)
	}
	part, err = mr.NextPart()
	if err != nil {
		return nil, nil, false, fmt.Errorf("no media part: %w", err)
	}
	content, err := io.ReadAll(part)
	if err != nil {
		return nil, nil, false, err
	}
	return &meta, content, true, nil
}

func (e *Emulator) waitIfHeld() {
	e.mu.Lock()
	ch := e.hold
	if ch != nil {
		e.waiting++
		e.maxWaiting = max(e.maxWaiting, e.waiting)
	}
	e.mu.Unlock()
	if ch == nil {
		return
	}
	<-ch
	e.mu.Lock()
	e.waiting--
	e.mu.Unlock()
}

func (e *Emulator) handleCreate(w http.ResponseWriter, r *http.Request) {
	e.cutSimple(r)
	meta, content, _, err := readUpload(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "badRequest", err.Error())
		return
	}
	if r.URL.Query().Get("uploadType") == "resumable" {
		e.startSession(w, r, &session{create: true, meta: meta})
		return
	}
	e.waitIfHeld()
	e.mu.Lock()
	defer e.mu.Unlock()
	if no := e.injected(meta.Name); no != nil {
		no.write(w)
		return
	}
	if no := e.checkCreate(meta); no != nil {
		no.write(w)
		return
	}
	e.answer(w, e.commitCreate(meta, content))
}

func (e *Emulator) handleUploadUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	e.cutSimple(r)
	meta, content, withMedia, err := readUpload(r)
	if err == nil && r.URL.Query().Get("uploadType") == "resumable" {
		e.startSession(w, r, &session{id: id, meta: meta})
		return
	}
	if err != nil || !withMedia {
		writeError(w, http.StatusBadRequest, "badRequest", fmt.Sprintf("update without media: %v", err))
		return
	}
	e.waitIfHeld()
	e.mu.Lock()
	defer e.mu.Unlock()
	old, ok := e.target(w, id)
	if !ok {
		return
	}
	if old.CannotModify {
		denied().write(w)
		return
	}
	e.answer(w, e.commitUpdate(old, meta, content))
}
