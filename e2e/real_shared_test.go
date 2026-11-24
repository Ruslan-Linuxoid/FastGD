//go:build e2e && realdrive

package e2e

import (
	"net/http"
	"net/url"
	"os"
	"slices"
	"testing"

	"github.com/Ruslan-Linuxoid/FastGD/internal/emulator"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestRealRootOfTheMount(t *testing.T) {
	r := testenv.RealDrive(t)
	d := newRealDaemon(t, r, startProxy(t))
	d.start()
	drives, err := r.Drive.Drives(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	got := dirNames(t, d.mnt)
	want := []string{"My Drive", "Shared with me"}
	if len(drives) > 0 {
		want = []string{"My Drive", "Shared drives", "Shared with me"}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("the root of the mount lists %q, want %q (%d shared drives)", got, want, len(drives))
	}
	shared := dirNames(t, d.path("/Shared with me"))
	for _, name := range shared {
		if _, err := os.Lstat(d.path("/Shared with me/" + name)); err != nil {
			t.Errorf("Shared with me lists %q, which does not stat: %v", name, err)
		}
	}

	items, shortcuts := collection(t, r)
	if len(shared) < items-shortcuts || len(shared) > items {
		t.Errorf("Shared with me lists %d items, the collection of Drive %d, %d of them shortcuts", len(shared),
			items, shortcuts)
	}
	t.Logf("Shared with me lists %d items, the collection of Drive %d (%d shortcuts); %d shared drives",
		len(shared), items, shortcuts, len(drives))
}

func collection(t *testing.T, r *testenv.Real) (items, shortcuts int) {
	t.Helper()
	q := url.Values{"q": {"sharedWithMe and trashed = false"}, "pageSize": {"1000"},
		"fields": {"nextPageToken,files(mimeType)"}}
	for {
		var page struct {
			NextPageToken string `json:"nextPageToken"`
			Files         []struct {
				MimeType string `json:"mimeType"`
			} `json:"files"`
		}
		if code, body := r.GetJSON("files", q, &page); code != http.StatusOK {
			t.Fatalf("the search of the collection: %d %s", code, body)
		}
		for _, f := range page.Files {
			items++
			if f.MimeType == emulator.MimeShortcut {
				shortcuts++
			}
		}
		if page.NextPageToken == "" {
			return items, shortcuts
		}
		q.Set("pageToken", page.NextPageToken)
	}
}
