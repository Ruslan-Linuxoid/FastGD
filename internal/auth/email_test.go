package auth

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTheEmailLiesBesideTheToken(t *testing.T) {
	token := filepath.Join(t.TempDir(), "token.json")
	if got := EmailFile(token); got != filepath.Join(filepath.Dir(token), "token.email") {
		t.Fatalf("the email of %s lies in %s", token, got)
	}
	if email, err := ReadEmail(token); err != nil || email != "" {
		t.Fatalf("before it is written the email reads %q, %v", email, err)
	}
	if err := SaveEmail(token, "you@example.com"); err != nil {
		t.Fatal(err)
	}
	if email, err := ReadEmail(token); err != nil || email != "you@example.com" {
		t.Fatalf("the email reads %q, %v", email, err)
	}
}

func TestNoClientFileIsTheClientOfTheApp(t *testing.T) {
	old := appClient
	t.Cleanup(func() { appClient = old })
	appClient = ""
	if _, err := Client(""); !errors.Is(err, ErrNoAppClient) || !strings.Contains(err.Error(), "client_id_file") {
		t.Fatalf("a build with no key of the app gives %v", err)
	}
	appClient = base64.StdEncoding.EncodeToString([]byte(`{"installed":{"client_id":"app-id","client_secret":"s",` +
		`"auth_uri":"https://accounts.google.com/o/oauth2/auth","token_uri":"https://oauth2.googleapis.com/token"}}`))
	c, err := Client("")
	if err != nil || c.ClientID != "app-id" || c.ClientSecret != "s" || len(c.Scopes) != 1 || c.Scopes[0] != Scope {
		t.Fatalf("with no client file the client is %+v, %v", c, err)
	}
}

func TestAClientFileWinsOverTheKeyOfTheBuild(t *testing.T) {
	old := appClient
	t.Cleanup(func() { appClient = old })
	appClient = base64.StdEncoding.EncodeToString(
		[]byte(`{"installed":{"client_id":"app-id","auth_uri":"a","token_uri":"t"}}`))
	own := filepath.Join(t.TempDir(), "client.json")
	if err := os.WriteFile(own, []byte(`{"installed":{"client_id":"own-id","auth_uri":"a","token_uri":"t"}}`),
		0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := Client(own); err != nil || c.ClientID != "own-id" {
		t.Fatalf("a client file of the config does not win over the key of the build: %+v, %v", c, err)
	}
}
