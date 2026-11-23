package emulator

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

type limits struct {
	quota  int
	window time.Duration
	spent  []spent

	refused int

	uploadLimit bool
	storageFull bool

	onChunks bool

	quotaFiles map[string]bool
	quotaAll   bool

	failCount  int
	failCode   int
	failReason string
	failAfter  time.Duration

	failPath, failPathReason string
	failPathCode             int
	failPathReads            bool
}

type perSecond struct {
	at   []time.Time
	peak int
}

func (p *perSecond) note(now time.Time) {
	i := 0
	for i < len(p.at) && now.Sub(p.at[i]) >= time.Second {
		i++
	}
	p.at = append(p.at[i:], now)
	p.peak = max(p.peak, len(p.at))
}

func (e *Emulator) RatePeaks() (reads, writes int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.reads.peak, e.writes.peak
}

type spent struct {
	at    time.Time
	units int
}

func (e *Emulator) SetRateQuota(units int, window time.Duration) {
	e.locked(func() { e.lim.quota, e.lim.window = units, window })
}

func (e *Emulator) RateRefusals() (n int) {
	e.locked(func() { n = e.lim.refused })
	return n
}

func (e *Emulator) SetUploadLimitReached(on bool) { e.locked(func() { e.lim.uploadLimit = on }) }

func (e *Emulator) SetStorageFull(on bool) { e.locked(func() { e.lim.storageFull = on }) }

func (e *Emulator) SetDownloadQuotaExceeded(on bool) { e.locked(func() { e.lim.quotaAll = on }) }

func (e *Emulator) SetFileDownloadQuotaExceeded(id string, on bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.lim.quotaFiles == nil {
		e.lim.quotaFiles = map[string]bool{}
	}
	e.lim.quotaFiles[id] = on
}

func (e *Emulator) overQuota(id string) bool { return e.lim.quotaAll || e.lim.quotaFiles[id] }

func writeDownloadQuota(w http.ResponseWriter) {
	writeError(w, http.StatusForbidden, "downloadQuotaExceeded", "The download quota for this file has been exceeded.")
}

func (e *Emulator) RefuseUploadsOnChunks(on bool) { e.locked(func() { e.lim.onChunks = on }) }

func (e *Emulator) FailPath(path string, code int, reason string) {
	e.locked(func() {
		e.lim.failPath, e.lim.failPathCode, e.lim.failPathReason, e.lim.failPathReads = path, code, reason, true
	})
}

func (e *Emulator) FailChanges(path string, code int, reason string) {
	e.locked(func() {
		e.lim.failPath, e.lim.failPathCode, e.lim.failPathReason, e.lim.failPathReads = path, code, reason, false
	})
}

func (e *Emulator) FailRequests(n, code int, reason string, after time.Duration) {
	e.locked(func() { e.lim.failCount, e.lim.failCode, e.lim.failReason, e.lim.failAfter = n, code, reason, after })
}

const (
	costMedia = 200
	costList  = 100
	costWrite = 50
	costOther = 5
)

func cost(r *http.Request) int {
	switch {

	case r.Method == http.MethodGet && r.URL.Query().Get("alt") == "media":
		return costMedia
	case r.Method == http.MethodGet && (r.URL.Path == "/drive/v3/files" || r.URL.Path == "/drive/v3/changes"):
		return costList
	case r.Method == http.MethodPost || r.Method == http.MethodPatch:
		return costWrite
	}
	return costOther
}

func (e *Emulator) refuse(w http.ResponseWriter, r *http.Request) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	l := &e.lim
	return l.injected(w, r) || l.uploadRefused(w, r) || l.overRate(w, r)
}

func (l *limits) injected(w http.ResponseWriter, r *http.Request) bool {
	if l.failPath != "" && r.URL.Path == l.failPath && (l.failPathReads || r.Method != http.MethodGet) {
		writeError(w, l.failPathCode, l.failPathReason, "injected failure")
		return true
	}
	if l.failCount > 0 {
		l.failCount--
		if l.failAfter > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int(l.failAfter/time.Second)))
		}
		writeError(w, l.failCode, l.failReason, "injected failure")
		return true
	}
	return false
}

func (l *limits) uploadRefused(w http.ResponseWriter, r *http.Request) bool {

	chunk := r.Method == http.MethodPut && !strings.HasPrefix(r.Header.Get("Content-Range"), "bytes */")
	upload := strings.HasPrefix(r.URL.Path, "/upload/")
	if l.onChunks {
		upload = upload && chunk
	} else {
		upload = upload && r.Method != http.MethodPut
	}
	switch {
	case upload && l.storageFull:
		writeError(w, http.StatusForbidden, "storageQuotaExceeded", "The user's Drive storage quota has been exceeded.")
		return true
	case upload && l.uploadLimit:
		writeError(w, http.StatusForbidden, "userRateLimitExceeded", "User rate limit exceeded.")
		return true
	}
	return false
}

func (l *limits) overRate(w http.ResponseWriter, r *http.Request) bool {
	if l.quota == 0 {
		return false
	}
	now := time.Now()
	used, i := 0, 0
	for i < len(l.spent) && now.Sub(l.spent[i].at) >= l.window {
		i++
	}
	l.spent = l.spent[i:]
	for _, s := range l.spent {
		used += s.units
	}
	c := cost(r)
	if used+c > l.quota {

		l.refused++
		if l.refused%2 == 0 {
			writeError(w, http.StatusTooManyRequests, "rateLimitExceeded", "Rate Limit Exceeded")
		} else {
			writeError(w, http.StatusForbidden, "userRateLimitExceeded", "User Rate Limit Exceeded.")
		}
		return true
	}
	l.spent = append(l.spent, spent{now, c})
	return false
}
