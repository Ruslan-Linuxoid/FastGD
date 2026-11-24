package emulator

import (
	"errors"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"time"

	drive "google.golang.org/api/drive/v3"
)

type session struct {
	create bool
	id     string
	meta   *drive.File
	size   int64
	data   []byte

	doneID string
}

func (e *Emulator) held(s *session) int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return int64(len(s.data))
}

func (e *Emulator) SetUploadRate(bytesPerSecond int64) { e.uploadRate.Store(bytesPerSecond) }

func (e *Emulator) SetUploadDelay(d time.Duration) { e.uploadDelay.Store(int64(d)) }

func (e *Emulator) answerLate(w http.ResponseWriter, r *http.Request, d time.Duration) {
	rec := httptest.NewRecorder()
	e.serve(rec, r)
	time.Sleep(d)
	maps.Copy(w.Header(), rec.Header())
	w.WriteHeader(rec.Code)
	_, _ = w.Write(rec.Body.Bytes())
}

func (e *Emulator) CutUploads(count, n int) {
	e.locked(func() { e.cutChunks = cuts{count, n} })
}

func (e *Emulator) ExpireUploadSessions() { e.locked(func() { clear(e.sessions) }) }

func (e *Emulator) SessionBytes() int64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	var n int64
	for _, s := range e.sessions {
		n += int64(len(s.data))
	}
	return n
}

func (e *Emulator) startSession(w http.ResponseWriter, r *http.Request, s *session) {
	size, err := strconv.ParseInt(r.Header.Get("X-Upload-Content-Length"), 10, 64)
	if err != nil || size < 0 {
		writeError(w, http.StatusBadRequest, "badRequest", "no X-Upload-Content-Length")
		return
	}
	s.size = size

	if e.contentDir != "" {
		s.data = e.room(size)
	}
	if s.create && s.meta.MimeType == "" {
		s.meta.MimeType = r.Header.Get("X-Upload-Content-Type")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if s.create {
		if no := e.checkCreate(s.meta); no != nil {
			no.write(w)
			return
		}
	} else if f, ok := e.files[s.id]; !ok {
		notFound(w, s.id)
		return
	} else if f.CannotModify {
		denied().write(w)
		return
	}
	uid := e.newID()
	e.sessions[uid] = s
	w.Header().Set("Location", "http://"+r.Host+r.URL.Path+"?uploadType=resumable&upload_id="+uid)
	w.WriteHeader(http.StatusOK)
}

func (e *Emulator) handleChunk(w http.ResponseWriter, r *http.Request) {
	uid := r.URL.Query().Get("upload_id")
	e.mu.Lock()
	s := e.sessions[uid]
	e.mu.Unlock()
	if s == nil {
		writeError(w, http.StatusNotFound, "notFound", "no upload session "+uid)
		return
	}
	if e.answerDone(w, s) {
		return
	}
	from, end, status, ok := checkedChunkRange(w, r, s)
	if !ok {
		return
	}
	if !status && !e.takeChunk(w, r, s, from, end) {
		return
	}
	if e.answerDone(w, s) {
		return
	}
	e.replyHeld(w, uid, s)
}

func (e *Emulator) answerDone(w http.ResponseWriter, s *session) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if s.doneID == "" {
		return false
	}
	if f := e.files[s.doneID]; f != nil {
		writeJSON(w, e.uploaded(f))
	} else {
		notFound(w, s.doneID)
	}
	return true
}

const chunkUnit = 256 << 10

const statusResumeIncomplete = 308

const chunkRead = 16 << 10

func (e *Emulator) readChunk(r *http.Request) ([]byte, bool) {
	limit := -1
	e.mu.Lock()
	if after, ok := e.cutChunks.next(); ok {
		limit = after
	}
	e.mu.Unlock()

	body := e.room(max(r.ContentLength, 0))
	buf := make([]byte, chunkRead)
	for {
		want := len(buf)
		if limit >= 0 {
			want = min(want, limit-len(body))
			if want == 0 {
				return body, true
			}
		}
		n, err := r.Body.Read(buf[:want])
		if rate := e.uploadRate.Load(); rate > 0 && n > 0 {
			time.Sleep(time.Until(e.uploadLink.slot(n, rate)))
		}
		body = append(body, buf[:n]...)
		if errors.Is(err, io.EOF) {
			return body, false
		}
		if err != nil {
			return body, true
		}
	}
}

func (e *Emulator) finish(w http.ResponseWriter, uid string, s *session) {
	e.waitIfHeld()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.sessions[uid] != s || s.doneID != "" {
		writeError(w, http.StatusNotFound, "notFound", "no upload session "+uid)
		return
	}
	name := s.meta.Name
	var old *File
	if !s.create {
		if old = e.files[s.id]; old == nil {
			notFound(w, s.id)
			return
		}
		name = old.Name
	}
	if no := e.injected(name); no != nil {
		no.write(w)
		return
	}
	var f *File
	if s.create {
		if no := e.checkCreate(s.meta); no != nil {
			no.write(w)
			return
		}
		f = e.commitCreate(s.meta, s.data)
	} else {
		f = e.commitUpdate(old, s.meta, s.data)
	}
	s.doneID, s.data = f.ID, nil
	e.answer(w, f)
}

func parseChunkRange(h string) (from, end, size int64, status bool, err error) {
	spec, ok := strings.CutPrefix(h, "bytes ")
	if !ok {
		return 0, 0, 0, false, errors.New("no bytes unit")
	}
	rng, tot, ok := strings.Cut(spec, "/")
	if !ok {
		return 0, 0, 0, false, errors.New("no size")
	}
	if size, err = strconv.ParseInt(tot, 10, 64); err != nil {
		return 0, 0, 0, false, err
	}
	if rng == "*" {
		return 0, 0, size, true, nil
	}
	a, b, ok := strings.Cut(rng, "-")
	if !ok {
		return 0, 0, 0, false, errors.New("no range")
	}
	if from, err = strconv.ParseInt(a, 10, 64); err != nil {
		return 0, 0, 0, false, err
	}
	last, err := strconv.ParseInt(b, 10, 64)
	return from, last + 1, size, false, err
}
