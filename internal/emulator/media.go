package emulator

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

func (e *Emulator) serveMedia(w http.ResponseWriter, r *http.Request, content []byte) {
	time.Sleep(time.Duration(e.mediaDelay.Load()))
	var code int
	var why string
	e.locked(func() { code, why = e.mediaFault, e.mediaFaultWhy })
	if code != 0 {
		writeError(w, code, why, "The download of the content fails: "+why)
		return
	}
	size := int64(len(content))
	start, end, status, ok := mediaRange(w, r, size)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.FormatInt(end-start+1, 10))
	w.WriteHeader(status)
	if size == 0 {
		return
	}
	e.sendBody(w, r, content[start:end+1])
}

func mediaRange(w http.ResponseWriter, r *http.Request, size int64) (start, end int64, status int, ok bool) {
	start, end, status = 0, size-1, http.StatusOK
	h := r.Header.Get("Range")
	if h == "" {
		return start, end, status, true
	}
	from, to, ok := parseRange(h, size)
	if !ok {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", size))
		writeError(w, http.StatusRequestedRangeNotSatisfiable, "requestedRangeNotSatisfiable", "bad range "+h)
		return 0, 0, 0, false
	}
	w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", from, to, size))
	return from, to, http.StatusPartialContent, true
}

func (e *Emulator) sendBody(w http.ResponseWriter, r *http.Request, body []byte) {
	e.mu.Lock()
	stall := e.stallAfter
	e.mu.Unlock()
	if stall >= 0 && stall < len(body) {
		_, _ = w.Write(body[:stall])
		_ = http.NewResponseController(w).Flush()

		<-r.Context().Done()
		return
	}
	if cut, ok := e.takeCut(); ok && cut < len(body) {
		_, _ = w.Write(body[:cut])
		_ = http.NewResponseController(w).Flush()
		var fn func()
		e.locked(func() { fn = e.onMediaCut })
		if fn != nil {
			fn()
		}

		panic(http.ErrAbortHandler)
	}
	e.writeBody(w, body)
}

func (e *Emulator) writeBody(w http.ResponseWriter, body []byte) {
	const chunk = 16 << 10
	for sent := 0; sent < len(body); {
		n := min(chunk, len(body)-sent)
		if rate := e.mediaRate.Load(); rate > 0 {
			time.Sleep(time.Until(e.mediaLink.slot(n, rate)))
		}
		if _, err := w.Write(body[sent : sent+n]); err != nil {
			return
		}
		sent += n
	}
}

type link struct {
	mu sync.Mutex

	free time.Time
}

func (l *link) slot(n int, rate int64) time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	from := l.free
	if now := time.Now(); from.Before(now) {
		from = now
	}
	l.free = from.Add(time.Duration(float64(n) / float64(rate) * float64(time.Second)))
	return l.free
}

func (e *Emulator) StallMedia(n int) { e.locked(func() { e.stallAfter = n }) }

func (e *Emulator) CutMedia(count, n int) { e.locked(func() { e.cutMedia = cuts{count, n} }) }

func (e *Emulator) FailMedia(code int, reason string) {
	e.locked(func() { e.mediaFault, e.mediaFaultWhy = code, reason })
}

func (e *Emulator) OnMediaCut(fn func()) { e.locked(func() { e.onMediaCut = fn }) }

func (e *Emulator) takeCut() (int, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cutMedia.next()
}

func parseRange(h string, size int64) (start, end int64, ok bool) {
	spec, found := strings.CutPrefix(h, "bytes=")
	if !found || strings.Contains(spec, ",") {
		return 0, 0, false
	}
	from, to, found := strings.Cut(spec, "-")
	if !found {
		return 0, 0, false
	}
	start, err := strconv.ParseInt(from, 10, 64)
	if err != nil || start < 0 || start >= size {
		return 0, 0, false
	}
	end = size - 1
	if to != "" {
		e, err := strconv.ParseInt(to, 10, 64)
		if err != nil || e < start {
			return 0, 0, false
		}
		end = min(e, size-1)
	}
	return start, end, true
}
