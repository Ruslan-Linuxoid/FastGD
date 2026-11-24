package emulator

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
)

func TestARefusedMoveOutOfADriveChangesNothing(t *testing.T) {
	t.Parallel()
	e := New()
	team := e.AddSharedDrive("Team")
	id := e.WriteFile(team, "fixed.txt", []byte("f"))
	e.Update(id, func(f *File) { f.CannotMoveOutOfDrive = true })
	target := "/drive/v3/files/" + id + "?supportsAllDrives=true&addParents=" + e.Root() + "&removeParents=" + team
	req := httptest.NewRequestWithContext(t.Context(), http.MethodPatch, target, strings.NewReader("{}"))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("the move out of the drive got %d, want 403", rec.Code)
	}
	if f, _ := e.Get(id); !slices.Equal(f.Parents, []string{team}) || f.DriveID != team {
		t.Fatalf("after the refusal the item lies under %v on the drive %q", f.Parents, f.DriveID)
	}
}
