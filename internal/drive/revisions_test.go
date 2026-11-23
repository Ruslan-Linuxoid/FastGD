package drive

import (
	"net/http"
	"testing"

	"google.golang.org/api/googleapi"
)

func TestARefusalForTheRightsIsTold(t *testing.T) {
	t.Parallel()
	for reason, want := range map[string]bool{
		"downloadRestrictedForRevision": true, "download_restricted_for_revision": true,
		"insufficientFilePermissions": true, "insufficientPermissions": false, "authError": false,
		"userRateLimitExceeded": false, "downloadQuotaExceeded": false, "cannotDownloadAbusiveFile": false,
		"cannotDownloadFile": false,
	} {
		err := &googleapi.Error{Code: http.StatusForbidden, Errors: []googleapi.ErrorItem{{Reason: reason}}}
		if got := refusedForRights(err); got != want {
			t.Errorf("a 403 with the reason %s is told as refused for the rights: %v, want %v", reason, got, want)
		}
	}
	if refusedForRights(&googleapi.Error{Code: http.StatusNotFound,
		Errors: []googleapi.ErrorItem{{Reason: "insufficientFilePermissions"}}}) {
		t.Error("a 404 is told as refused for the rights")
	}
}
