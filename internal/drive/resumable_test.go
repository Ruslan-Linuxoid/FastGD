package drive

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestASessionAddressNotOfDriveIsCalledNever(t *testing.T) {
	t.Parallel()
	var called atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called.Add(1) }))
	defer other.Close()
	drv := httptest.NewServer(http.NotFoundHandler())
	defer drv.Close()
	c, err := New(t.Context(), Options{BaseURL: drv.URL, HTTPClient: &http.Client{}})
	if err != nil {
		t.Fatal(err)
	}
	session := other.URL + "/upload/drive/v3/files?upload_id=1"
	if _, err := c.SendChunk(t.Context(), session, 0, 4, 4, strings.NewReader("data")); !errors.Is(err, ErrSessionGone) {
		t.Errorf("a chunk to a session not of Drive ends with %v, want ErrSessionGone", err)
	}
	if _, err := c.UploadStatus(t.Context(), session, 4); !errors.Is(err, ErrSessionGone) {
		t.Errorf("the status of a session not of Drive ends with %v, want ErrSessionGone", err)
	}
	if n := called.Load(); n != 0 {
		t.Fatalf("the address not of Drive got %d requests", n)
	}
}

func TestARedirectOutOfDriveIsNotFollowed(t *testing.T) {
	t.Parallel()
	var called atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called.Add(1) }))
	defer other.Close()
	drv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/x", http.StatusTemporaryRedirect)
	}))
	defer drv.Close()
	c, err := New(t.Context(), Options{BaseURL: drv.URL, HTTPClient: &http.Client{}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.UploadStatus(t.Context(), drv.URL+"/upload/drive/v3/files?upload_id=1", 4)
	if err == nil || IsNetwork(err) {
		t.Errorf("the status of a session redirected out of Drive ends with %v, the network %v", err, IsNetwork(err))
	}
	if n := called.Load(); n != 0 {
		t.Fatalf("the address not of Drive got %d requests", n)
	}
}

func TestASessionOpenedAtAnAddressNotOfDriveFails(t *testing.T) {
	t.Parallel()
	drv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", "https://upload.example.com/upload/drive/v3/files?upload_id=1")
		w.WriteHeader(http.StatusOK)
	}))
	defer drv.Close()
	c, err := New(t.Context(), Options{BaseURL: drv.URL, HTTPClient: &http.Client{}})
	if err != nil {
		t.Fatal(err)
	}
	session, err := c.StartCreate(t.Context(), "id1", "a.txt", "parent", "text/plain", time.Now(), 4)
	if err == nil {
		t.Fatalf("a session opened at %q", session)
	}
}
