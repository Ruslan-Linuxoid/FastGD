//go:build realdrive

package drive_test

import (
	"net/http"
	"testing"

	"golang.org/x/oauth2"

	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
	"github.com/Ruslan-Linuxoid/FastGD/internal/testenv"
)

func TestRealMissingItemAndBadToken(t *testing.T) {
	r := testenv.RealDrive(t)
	_, err := r.Drive.Get(t.Context(), "1fastgdTestNoSuchItem0000000000000")
	t.Logf("files.get of an item Drive does not hold: %v", err)
	if err == nil || drive.IsRefusal(err) || !drive.IsDenied(err) {
		t.Errorf("a missing item: a refusal %v, denied %v", drive.IsRefusal(err), drive.IsDenied(err))
	}
	_, err = r.Drive.UpdateMeta(t.Context(), "1fastgdTestNoSuchItem0000000000000", drive.MetaUpdate{Name: "x"})
	t.Logf("files.update of an item Drive does not hold: %v", err)
	if err == nil || drive.IsRefusal(err) {
		t.Errorf("an update of a missing item is a refusal: %v", drive.IsRefusal(err))
	}

	bad := &http.Client{Transport: &oauth2.Transport{
		Source: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "not-a-token"}), Base: drive.Transport(),
	}}
	cl, err := drive.New(t.Context(), drive.Options{HTTPClient: bad})
	if err != nil {
		t.Fatal(err)
	}
	_, err = cl.RootID(t.Context())
	t.Logf("a call with a token Drive does not take: %v", err)
	if !drive.IsLoginExpired(err) || drive.IsRefusal(err) || drive.IsNetwork(err) {
		t.Errorf("a bad token: an expired login %v, a refusal %v, the network %v",
			drive.IsLoginExpired(err), drive.IsRefusal(err), drive.IsNetwork(err))
	}
}
