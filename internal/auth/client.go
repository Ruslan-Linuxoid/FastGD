package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"golang.org/x/oauth2"
)

const Scope = "https://www.googleapis.com/auth/drive"

type clientFile struct {
	Installed *struct {
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		AuthURI      string `json:"auth_uri"`
		TokenURI     string `json:"token_uri"`
	} `json:"installed"`
	Web *json.RawMessage `json:"web"`
}

func Client(path string) (*oauth2.Config, error) {
	if path == "" {
		return AppClient()
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("auth: client_id_file: %w", err)
	}
	return parseClient(data, "client_id_file "+path)
}

func parseClient(data []byte, what string) (*oauth2.Config, error) {
	var f clientFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("auth: %s is not the JSON of an OAuth client: %w", what, err)
	}
	if f.Installed == nil && f.Web != nil {
		return nil, fmt.Errorf("auth: %s is of a client of the type Web application, "+
			"the daemon needs one of the type Desktop app", what)
	}
	c := f.Installed
	if c == nil || c.ClientID == "" || c.AuthURI == "" || c.TokenURI == "" {
		return nil, errors.New("auth: " + what + " names no OAuth client of the type Desktop app")
	}
	return &oauth2.Config{
		ClientID:     c.ClientID,
		ClientSecret: c.ClientSecret,
		Endpoint:     oauth2.Endpoint{AuthURL: c.AuthURI, TokenURL: c.TokenURI, AuthStyle: oauth2.AuthStyleInParams},
		Scopes:       []string{Scope},
	}, nil
}
