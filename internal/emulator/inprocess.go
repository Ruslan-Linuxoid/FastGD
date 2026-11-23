package emulator

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"syscall"
)

func InProcess(e *Emulator) http.RoundTripper { return inProcess{e} }

type inProcess struct{ e *Emulator }

func (t inProcess) RoundTrip(r *http.Request) (resp *http.Response, err error) {
	if r.Body != nil {
		defer r.Body.Close()
	}

	req := r.Clone(r.Context())
	if req.Body == nil {
		req.Body = http.NoBody
	}
	rec := httptest.NewRecorder()
	defer func() {
		if p := recover(); p != nil {
			if p != http.ErrAbortHandler {
				panic(p)
			}
			resp, err = nil, fmt.Errorf("emulator: %w", syscall.ECONNRESET)
		}
	}()
	t.e.ServeHTTP(rec, req)
	resp = rec.Result()
	resp.Request = r
	return resp, nil
}
