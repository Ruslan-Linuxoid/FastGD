package emulator

import (
	"fmt"
	"net/http"
)

func checkedChunkRange(w http.ResponseWriter, r *http.Request, s *session) (from, end int64, status, ok bool) {
	from, end, size, status, err := parseChunkRange(r.Header.Get("Content-Range"))
	if err != nil || size != s.size {
		writeError(w, http.StatusBadRequest, "badRequest", fmt.Sprintf("bad Content-Range %q", r.Header.Get("Content-Range")))
		return 0, 0, false, false
	}
	if !status && end != size && end%chunkUnit != 0 {
		writeError(w, http.StatusBadRequest, "badRequest",
			fmt.Sprintf("a chunk that is not the last ends at %d, not at a multiple of %d", end, chunkUnit))
		return 0, 0, false, false
	}
	return from, end, status, true
}

func (e *Emulator) takeChunk(w http.ResponseWriter, r *http.Request, s *session, from, end int64) bool {
	if held := e.held(s); from > held {
		writeError(w, http.StatusBadRequest, "badRequest", fmt.Sprintf("chunk at %d leaves a gap after %d", from, held))
		return false
	}
	body, cut := e.readChunk(r)
	if !cut && int64(len(body)) != end-from {
		writeError(w, http.StatusBadRequest, "badRequest",
			fmt.Sprintf("the chunk carries %d bytes, its Content-Range names %d", len(body), end-from))
		e.free(body)
		return false
	}
	e.mu.Lock()

	if held := int64(len(s.data)); s.doneID == "" && from <= held && from+int64(len(body)) > held {
		s.data = append(s.data, body[held-from:]...)
	}
	e.mu.Unlock()
	e.free(body)
	if cut {

		panic(http.ErrAbortHandler)
	}
	return true
}

func (e *Emulator) replyHeld(w http.ResponseWriter, uid string, s *session) {
	held := e.held(s)
	if held < s.size {
		if held > 0 {
			w.Header().Set("Range", fmt.Sprintf("bytes=0-%d", held-1))
		}
		w.WriteHeader(statusResumeIncomplete)
		return
	}
	e.finish(w, uid, s)
}
