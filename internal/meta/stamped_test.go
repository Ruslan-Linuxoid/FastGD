package meta

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStatusShowsStampsWithABadTime(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "meta.db")
	writeStampsWithABadTime(t, path)
	ro, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()
	st, err := ro.ReadStatus(time.Now().Add(-24 * time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	requireStampsShownAsTheyWere(t, st)
}

func writeStampsWithABadTime(t *testing.T, path string) {
	t.Helper()
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for key, v := range map[string]string{
		StateUndone:          "soon Docs/a.txt: 400 bad",
		StateDownloadRefused: "soon refused-id",
		StateAbuseRefused:    "soon abusive-id",
		StateDiscarded:       "soon /copy: why",
	} {
		if err := s.SetState(key, v); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()
}

func requireStampsShownAsTheyWere(t *testing.T, st Status) {
	t.Helper()
	if !st.Undone.IsZero() || st.UndoneWhat != "Docs/a.txt: 400 bad" {
		t.Fatalf("undone shows at %v as %q", st.Undone, st.UndoneWhat)
	}
	if !st.DownloadRefused.IsZero() || st.RefusedPath != "refused-id" {
		t.Fatalf("the refused download shows at %v as %q", st.DownloadRefused, st.RefusedPath)
	}
	if !st.AbuseRefused.IsZero() || st.AbusivePath != "abusive-id" {
		t.Fatalf("the refused abusive file shows at %v as %q", st.AbuseRefused, st.AbusivePath)
	}
	if !st.Discarded.IsZero() || st.DiscardedWhat != "" {
		t.Fatalf("the setting aside shows at %v as %q", st.Discarded, st.DiscardedWhat)
	}
}
