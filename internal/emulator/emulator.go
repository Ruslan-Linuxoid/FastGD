package emulator

import (
	"crypto/md5"
	"encoding/hex"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Ruslan-Linuxoid/FastGD/internal/clock"
)

const (
	MimeFolder   = "application/vnd.google-apps.folder"
	MimeShortcut = "application/vnd.google-apps.shortcut"
	googlePrefix = "application/vnd.google-apps."
)

func writeAbusive(w http.ResponseWriter) {
	writeError(w, http.StatusForbidden, "cannotDownloadAbusiveFile",
		"This file has been identified as malware or spam and cannot be downloaded.")
}

func ViewLink(mimeType, id string) string {
	return "https://docs.google.com/" + strings.TrimPrefix(mimeType, googlePrefix) + "/d/" + id + "/edit"
}

const Email = "user@example.com"

func md5Hex(b []byte) string {

	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}

type Request struct {
	Method string
	Path   string
	Query  url.Values
	Range  string

	ContentRange string
}

type Emulator struct {
	mu    sync.Mutex
	clock clock.Clock
	rng   *rand.Rand
	root  string
	files map[string]*File

	order []string

	revs     map[string][]*revision
	revCount int

	changes []change

	onListPage func(n int)
	listPages  int

	reserved map[string]bool

	uploadFault UploadFault
	corrupt     int
	loseAnswers int

	noUploadMD5 bool

	onMediaCut func()

	parentless Parentless

	mediaFault    int
	mediaFaultWhy string

	hold       chan struct{}
	waiting    int
	maxWaiting int

	quotaLimit int64
	quotaUsage int64

	filesUsage, trashUsage int64
	requests               []Request
	mux                    *http.ServeMux
	offline                atomic.Bool

	mediaDelay atomic.Int64

	mediaRate atomic.Int64

	mediaLink  link
	uploadLink link

	uploadRate atomic.Int64

	uploadDelay atomic.Int64

	nextIDs []string

	reads, writes perSecond

	sessions map[string]*session

	cutChunks, cutSimples cuts

	lim limits

	cutMedia cuts

	stallAfter int
}

type Option func(*Emulator)

func WithClock(c clock.Clock) Option { return func(e *Emulator) { e.clock = c } }

func New(opts ...Option) *Emulator {
	e := &Emulator{
		clock: clock.Real{},

		rng:      rand.New(rand.NewPCG(1, 2)),
		files:    map[string]*File{},
		revs:     map[string][]*revision{},
		sessions: map[string]*session{},
		reserved: map[string]bool{},

		stallAfter: -1,
	}
	for _, o := range opts {
		o(e)
	}
	now := e.now()
	e.root = e.newID()
	e.files[e.root] = &File{ID: e.root, Name: "My Drive", MimeType: MimeFolder, Version: 1, Created: now, Modified: now}
	e.routes()
	return e
}

func requestTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {

		year, rest, ok := strings.Cut(s, "-")
		y, yerr := strconv.Atoi(year)
		if !ok || len(year) <= 4 || strings.Trim(year, "0123456789") != "" || yerr != nil {
			return time.Time{}, err
		}
		r, rerr := time.Parse(time.RFC3339Nano, "2000-"+rest)
		if rerr != nil {
			return time.Time{}, err
		}
		t = time.Date(y, r.Month(), r.Day(), r.Hour(), r.Minute(), r.Second(), r.Nanosecond(), r.Location())
		if t.Day() != r.Day() {

			return time.Time{}, err
		}
	}
	return t.UTC().Truncate(time.Millisecond), nil
}

func (e *Emulator) Root() string { return e.root }

func (e *Emulator) Mkdir(parent, name string) string {
	return e.Put(File{Name: name, MimeType: MimeFolder, Parents: []string{parent}})
}

func (e *Emulator) WriteGoogleFile(parent, name, mimeType string) string {
	return e.Put(File{Name: name, MimeType: mimeType, Parents: []string{parent}})
}

func (e *Emulator) WriteFile(parent, name string, content []byte) string {
	return e.Put(File{Name: name, MimeType: "application/octet-stream", Parents: []string{parent}, Content: content})
}

func (e *Emulator) OnListPage(fn func(n int)) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.onListPage = fn
}

func (e *Emulator) SetOffline(on bool) { e.offline.Store(on) }

func (e *Emulator) SetMediaDelay(d time.Duration) { e.mediaDelay.Store(int64(d)) }

func (e *Emulator) SetMediaRate(bytesPerSecond int64) { e.mediaRate.Store(bytesPerSecond) }

type cuts struct{ count, after int }

func (c *cuts) next() (after int, ok bool) {
	if c.count == 0 {
		return 0, false
	}
	if c.count > 0 {
		c.count--
	}
	return c.after, true
}

func (e *Emulator) locked(f func()) {
	e.mu.Lock()
	defer e.mu.Unlock()
	f()
}

func (e *Emulator) now() time.Time { return e.clock.Now().UTC().Truncate(time.Millisecond) }

const idChars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_"

func (e *Emulator) newID() string {
	var b strings.Builder
	b.WriteByte('1')
	for range 32 {
		b.WriteByte(idChars[e.rng.IntN(len(idChars))])
	}
	return b.String()
}
