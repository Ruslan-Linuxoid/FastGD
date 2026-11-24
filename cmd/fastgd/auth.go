package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"golang.org/x/oauth2"

	"github.com/Ruslan-Linuxoid/FastGD/internal/auth"
	"github.com/Ruslan-Linuxoid/FastGD/internal/drive"
)

var errNoKeyForAuth = errors.New("this build of FastGD carries no key of the app FastGD: pass -client-id, the JSON " +
	"of an OAuth client of your own of the type Desktop app; the README tells how to make one, under \"Your own key\"")

func authCmd(args []string) error {
	fl := flag.NewFlagSet("auth", flag.ContinueOnError)
	clientID := fl.String("client-id", "", "the JSON of an OAuth client of the type Desktop app; "+
		"without it the key of the app FastGD the build carries")
	token := fl.String("token", "", "the token file to write")
	listen := fl.String("listen", "127.0.0.1:0", "where the redirect of the sign-in is served")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if *token == "" {
		return errors.New("-token is required")
	}
	cfg, err := auth.Client(*clientID)
	if errors.Is(err, auth.ErrNoAppClient) {
		return errNoKeyForAuth
	}
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	hc := &http.Client{Transport: drive.Transport()}
	t, err := auth.Login(ctx, cfg, *listen, os.Stdout, hc)
	if err != nil {
		return err
	}

	email, eerr := accountEmail(ctx, cfg.Client(context.WithValue(ctx, oauth2.HTTPClient, hc), t))
	if eerr == nil {
		if err := auth.SaveEmail(*token, email); err != nil {
			return err
		}
	}
	if err := auth.SaveToken(*token, t); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, "the token is in", *token)
	if eerr != nil {
		return fmt.Errorf("the token is in %s, but Drive did not tell the email of the account: %w", *token, eerr)
	}
	fmt.Fprintln(os.Stdout, "the email of the account,", email+", is in", auth.EmailFile(*token))
	return nil
}

func accountEmail(ctx context.Context, hc *http.Client) (string, error) {
	cl, err := drive.New(ctx, drive.Options{HTTPClient: hc})
	if err != nil {
		return "", err
	}
	return askEmail(cl)(ctx)
}
