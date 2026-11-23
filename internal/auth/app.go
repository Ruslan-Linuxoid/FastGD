package auth

import (
	"encoding/base64"
	"errors"
	"fmt"

	"golang.org/x/oauth2"
)

var appClient string

var ErrNoAppClient = errors.New("auth: this build of FastGD carries no key of the app FastGD: name " +
	"client_id_file in the config, the JSON of an OAuth client of your own of the type Desktop app; " +
	"the README tells how to make one, under \"Your own key\"")

func AppClient() (*oauth2.Config, error) {
	if appClient == "" {
		return nil, ErrNoAppClient
	}
	data, err := base64.StdEncoding.DecodeString(appClient)
	if err != nil {
		return nil, fmt.Errorf("auth: the key of the app this build carries is no base64: %w", err)
	}
	return parseClient(data, "the key of the app this build carries")
}
